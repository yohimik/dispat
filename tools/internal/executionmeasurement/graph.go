package executionmeasurement

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

const (
	completedStatus                = "completed"
	successfulStatus               = "success"
	taskIntervalsPrefix            = "/taskIntervals/"
	durationSuffix                 = "/durationSeconds"
	sumDisjointRule                = "sum-disjoint"
	graphErrorFormat               = "execution measurement graph: %s"
	vertexErrorFormat              = "execution measurement vertex %q: %s"
	incompleteDurationReason       = "required operation has no complete successful duration"
	disconnectedDurationReason     = "duration aggregation lacks comparable, disjoint phase intervals"
	invalidGraphIdentityReason     = "vertices require unique nonempty identities and operations"
	invalidGraphEdgeReason         = "edges require known endpoints, a prerequisite kind and a unique typed identity"
	cyclicGraphReason              = "retained edges form a cycle"
	invalidVirtualDurationReason   = "a virtual relay cannot carry physical duration inputs"
	invalidDurationReferenceReason = "duration input must identify a retained interval for this task"
)

type graphValues struct {
	counts          GraphCounts
	durations       map[string]float64
	order           []string
	work            float64
	criticalPath    float64
	path            []string
	reasons         []string
	violations      []string
	schedulePremise schedulePremise
}

func deriveGraph(measurement *Measurement) (graphValues, error) {
	result := graphValues{durations: map[string]float64{}}
	vertices := map[string]Vertex{}
	indegrees := map[string]int{}
	successors := map[string][]string{}
	for _, vertex := range measurement.Graph.Vertices {
		if _, exists := vertices[vertex.ID]; exists || vertex.ID == "" || vertex.Operation == "" {
			return result, fmt.Errorf(graphErrorFormat, invalidGraphIdentityReason)
		}
		vertices[vertex.ID] = vertex
		indegrees[vertex.ID] = 0
		result.counts.Vertices++
		if vertex.IsVirtual {
			if len(vertex.DurationInputs) != 0 {
				return result, fmt.Errorf(vertexErrorFormat, vertex.ID, invalidVirtualDurationReason)
			}
			result.counts.VirtualVertices++
			result.durations[vertex.ID] = 0
			continue
		}
		duration, reason, err := vertexDuration(measurement, vertex)
		if err != nil {
			return result, err
		}
		if reason != "" {
			result.reasons = append(result.reasons, vertex.ID+": "+reason)
			continue
		}
		result.counts.TimedVertices++
		result.durations[vertex.ID] = duration
	}
	edges := map[Edge]bool{}
	for _, edge := range measurement.Graph.Edges {
		_, fromExists := vertices[edge.From]
		_, toExists := vertices[edge.To]
		if !fromExists || !toExists || edge.Kind == "" || edges[edge] {
			return result, fmt.Errorf(graphErrorFormat, invalidGraphEdgeReason)
		}
		edges[edge] = true
		indegrees[edge.To]++
		successors[edge.From] = append(successors[edge.From], edge.To)
		result.counts.Edges++
	}
	queue := []string{}
	for _, vertex := range measurement.Graph.Vertices {
		if indegrees[vertex.ID] == 0 {
			queue = append(queue, vertex.ID)
		}
	}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		result.order = append(result.order, current)
		for _, successor := range successors[current] {
			indegrees[successor]--
			if indegrees[successor] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	if len(result.order) != len(vertices) {
		return result, fmt.Errorf(graphErrorFormat, cyclicGraphReason)
	}
	result.violations = findDurationViolations(measurement)
	for _, interval := range measurement.TaskIntervals {
		if _, exists := vertices[interval.TaskID]; !exists {
			return result, fmt.Errorf(vertexErrorFormat, interval.TaskID, invalidDurationReferenceReason)
		}
	}
	result.schedulePremise = classifySchedulePremise(measurement, len(result.durations) == len(vertices))
	if len(result.reasons) != 0 {
		return result, nil
	}
	finishes := map[string]float64{}
	paths := map[string][]string{}
	for _, current := range result.order {
		duration := result.durations[current]
		result.work += duration
		finishes[current] += duration
		paths[current] = append(paths[current], current)
		if finishes[current] > result.criticalPath || len(result.path) == 0 {
			result.criticalPath = finishes[current]
			result.path = slices.Clone(paths[current])
		}
		for _, successor := range successors[current] {
			if finishes[current] > finishes[successor] || len(paths[successor]) == 0 {
				finishes[successor] = finishes[current]
				paths[successor] = slices.Clone(paths[current])
			}
		}
	}
	return result, nil
}

func vertexDuration(measurement *Measurement, vertex Vertex) (float64, string, error) {
	if len(vertex.DurationInputs) == 0 {
		return 0, incompleteDurationReason, nil
	}
	intervals := []TaskInterval{}
	seen := map[int]bool{}
	total := 0.0
	isComplete := true
	for _, reference := range vertex.DurationInputs {
		indexText := strings.TrimSuffix(strings.TrimPrefix(reference.Pointer, taskIntervalsPrefix), durationSuffix)
		index, err := strconv.Atoi(indexText)
		if reference.Source != measurementSource || reference.Pointer != taskIntervalsPrefix+strconv.Itoa(index)+durationSuffix || err != nil || index < 0 || index >= len(measurement.TaskIntervals) || seen[index] {
			return 0, "", fmt.Errorf(vertexErrorFormat, vertex.ID, invalidDurationReferenceReason)
		}
		seen[index] = true
		interval := measurement.TaskIntervals[index]
		if interval.TaskID != vertex.ID {
			return 0, "", fmt.Errorf(vertexErrorFormat, vertex.ID, invalidDurationReferenceReason)
		}
		if interval.DurationSeconds == nil || (interval.Status != completedStatus && interval.Status != successfulStatus) {
			isComplete = false
			continue
		}
		if !isFiniteNonnegative(*interval.DurationSeconds) {
			return 0, "", fmt.Errorf(vertexErrorFormat, vertex.ID, incompleteDurationReason)
		}
		total += *interval.DurationSeconds
		intervals = append(intervals, interval)
	}
	if !isComplete {
		return 0, incompleteDurationReason, nil
	}
	if len(vertex.DurationInputs) > 1 && vertex.DurationRule != sumDisjointRule {
		return 0, disconnectedDurationReason, nil
	}
	for index, interval := range intervals {
		if len(intervals) == 1 {
			break
		}
		if interval.StartSeconds == nil || interval.EndSeconds == nil {
			return 0, disconnectedDurationReason, nil
		}
		for _, previous := range intervals[:index] {
			if previous.ClockDomain != interval.ClockDomain || previous.ClockKind != interval.ClockKind || (*previous.EndSeconds > *previous.StartSeconds && *interval.EndSeconds > *interval.StartSeconds && *previous.StartSeconds < *interval.EndSeconds && *interval.StartSeconds < *previous.EndSeconds) {
				return 0, disconnectedDurationReason, nil
			}
		}
	}
	return total, "", nil
}

func isFiniteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
