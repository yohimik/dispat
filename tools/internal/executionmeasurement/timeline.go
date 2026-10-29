package executionmeasurement

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

const (
	taskSpanID                = "task-span"
	unmappedTimelineReason    = "task span/overlap requires complete coordinates on one comparable monotonic timeline"
	incorrectTimelineReason   = "task span/overlap differs from retained intervals"
	capacityViolationReason   = "comparable retained intervals exceed the declared task-slot ceiling"
	precedenceViolationReason = "comparable retained intervals violate modeled precedence, including virtual-relay paths"
	phaseOverlapReason        = "sum-disjoint duration inputs contain overlapping selected phases"
)

type endpoint struct {
	seconds float64
	delta   int
}

func validateComparableTimeline(measurement *Measurement) error {
	intervals := selectModeledIntervals(measurement)
	var taskSpan *float64
	for _, window := range measurement.TimeWindows {
		if window.ID == taskSpanID {
			taskSpan = window.Seconds
			if taskSpan != nil && (len(intervals) == 0 || window.ClockDomain != intervals[0].ClockDomain || window.ClockKind != monotonicClock) {
				return fmt.Errorf(invalidMeasurementFormat, taskSpanID, unmappedTimelineReason)
			}
		}
	}
	if taskSpan == nil && measurement.ObservedPeakOverlap.Value == nil {
		return nil
	}
	graph, err := deriveGraph(measurement)
	if err != nil {
		return err
	}
	if len(graph.reasons) > 0 {
		return fmt.Errorf(invalidMeasurementFormat, taskSpanID, unmappedTimelineReason)
	}
	if len(intervals) == 0 {
		if taskSpan == nil && measurement.ObservedPeakOverlap.Value != nil && *measurement.ObservedPeakOverlap.Value == 0 {
			return nil
		}
		return fmt.Errorf(invalidMeasurementFormat, taskSpanID, unmappedTimelineReason)
	}
	domain := intervals[0].ClockDomain
	start, end := math.Inf(1), math.Inf(-1)
	endpoints := []endpoint{}
	for _, interval := range intervals {
		if interval.StartSeconds == nil || interval.EndSeconds == nil || interval.ClockDomain != domain || interval.ClockKind != monotonicClock {
			return fmt.Errorf(invalidMeasurementFormat, taskSpanID, unmappedTimelineReason)
		}
		start = math.Min(start, *interval.StartSeconds)
		end = math.Max(end, *interval.EndSeconds)
		if *interval.EndSeconds > *interval.StartSeconds {
			endpoints = append(endpoints, endpoint{*interval.StartSeconds, 1}, endpoint{*interval.EndSeconds, -1})
		}
	}
	peak := calculatePeak(endpoints)
	if (taskSpan != nil && !nearlyEqual(*taskSpan, end-start)) || (measurement.ObservedPeakOverlap.Value != nil && *measurement.ObservedPeakOverlap.Value != float64(peak)) {
		return fmt.Errorf(invalidMeasurementFormat, taskSpanID, incorrectTimelineReason)
	}
	return nil
}

func calculatePeak(endpoints []endpoint) int {
	sort.Slice(endpoints, func(left, right int) bool {
		if endpoints[left].seconds == endpoints[right].seconds {
			return endpoints[left].delta < endpoints[right].delta
		}
		return endpoints[left].seconds < endpoints[right].seconds
	})
	current, peak := 0, 0
	for _, point := range endpoints {
		current += point.delta
		peak = max(peak, current)
	}
	return peak
}

func findScheduleViolations(measurement *Measurement, order []string) []string {
	violations := []string{}
	byDomain := modeledEndpointsByDomain(selectModeledIntervals(measurement))
	byTask := map[string][]TaskInterval{}
	for _, interval := range selectModeledIntervals(measurement) {
		if interval.ClockKind != monotonicClock || interval.StartSeconds == nil || interval.EndSeconds == nil {
			continue
		}
		byTask[interval.TaskID] = append(byTask[interval.TaskID], interval)
	}
	if measurement.Resources.TaskSlotLimit.Value != nil {
		for _, endpoints := range byDomain {
			if float64(calculatePeak(endpoints)) > *measurement.Resources.TaskSlotLimit.Value {
				violations = append(violations, capacityViolationReason)
				break
			}
		}
	}
	predecessorFinishes := map[string]map[string]float64{}
	isPrecedenceViolated := false
	for _, identity := range order {
		if predecessorFinishes[identity] == nil {
			predecessorFinishes[identity] = map[string]float64{}
		}
		for _, interval := range byTask[identity] {
			if required, exists := predecessorFinishes[identity][interval.ClockDomain]; exists && *interval.StartSeconds < required {
				isPrecedenceViolated = true
			}
		}
		for _, interval := range byTask[identity] {
			previous, exists := predecessorFinishes[identity][interval.ClockDomain]
			if !exists || *interval.EndSeconds > previous {
				predecessorFinishes[identity][interval.ClockDomain] = *interval.EndSeconds
			}
		}
		for _, edge := range measurement.Graph.Edges {
			if edge.From != identity {
				continue
			}
			if predecessorFinishes[edge.To] == nil {
				predecessorFinishes[edge.To] = map[string]float64{}
			}
			for domain, end := range predecessorFinishes[identity] {
				previous, exists := predecessorFinishes[edge.To][domain]
				if !exists || end > previous {
					predecessorFinishes[edge.To][domain] = end
				}
			}
		}
	}
	if isPrecedenceViolated {
		violations = append(violations, precedenceViolationReason)
	}
	return violations
}

func modeledEndpointsByDomain(intervals []TaskInterval) map[string][]endpoint {
	groups := map[string][]TaskInterval{}
	for _, interval := range intervals {
		if interval.ClockKind != monotonicClock || interval.StartSeconds == nil || interval.EndSeconds == nil || *interval.EndSeconds <= *interval.StartSeconds {
			continue
		}
		identity := interval.ClockDomain + "\x00" + interval.TaskID + "\x00" + interval.AttemptID
		groups[identity] = append(groups[identity], interval)
	}
	domains := map[string][]endpoint{}
	for _, phases := range groups {
		sort.Slice(phases, func(left, right int) bool { return *phases[left].StartSeconds < *phases[right].StartSeconds })
		domain := phases[0].ClockDomain
		start, end := *phases[0].StartSeconds, *phases[0].EndSeconds
		for _, phase := range phases[1:] {
			if *phase.StartSeconds <= end {
				end = math.Max(end, *phase.EndSeconds)
				continue
			}
			domains[domain] = append(domains[domain], endpoint{start, 1}, endpoint{end, -1})
			start, end = *phase.StartSeconds, *phase.EndSeconds
		}
		domains[domain] = append(domains[domain], endpoint{start, 1}, endpoint{end, -1})
	}
	return domains
}

func selectModeledIntervals(measurement *Measurement) []TaskInterval {
	selected := map[string]bool{}
	for _, vertex := range measurement.Graph.Vertices {
		if !vertex.IsVirtual {
			for _, reference := range vertex.DurationInputs {
				selected[reference.Pointer] = true
			}
		}
	}
	intervals := []TaskInterval{}
	for index, interval := range measurement.TaskIntervals {
		if selected[taskIntervalsPrefix+strconv.Itoa(index)+durationSuffix] && interval.DurationSeconds != nil && (interval.Status == completedStatus || interval.Status == successfulStatus) {
			intervals = append(intervals, interval)
		}
	}
	return intervals
}

func hasContainmentViolation(measurement *Measurement, window TimeWindow) bool {
	if window.Seconds == nil || window.ClockKind != monotonicClock {
		return false
	}
	start, end := math.Inf(1), math.Inf(-1)
	for _, interval := range selectModeledIntervals(measurement) {
		if interval.ClockKind != monotonicClock || interval.ClockDomain != window.ClockDomain {
			continue
		}
		if interval.DurationSeconds != nil && *interval.DurationSeconds > *window.Seconds {
			return true
		}
		if interval.StartSeconds == nil || interval.EndSeconds == nil {
			continue
		}
		start, end = math.Min(start, *interval.StartSeconds), math.Max(end, *interval.EndSeconds)
	}
	return end-start > *window.Seconds
}

func findDurationViolations(measurement *Measurement) []string {
	violations := []string{}
	intervals := selectModeledIntervals(measurement)
	for _, vertex := range measurement.Graph.Vertices {
		if vertex.DurationRule != sumDisjointRule {
			continue
		}
		isOverlapping := false
		for index, interval := range intervals {
			if interval.TaskID != vertex.ID || interval.StartSeconds == nil || interval.EndSeconds == nil {
				continue
			}
			for _, previous := range intervals[:index] {
				if previous.TaskID == vertex.ID && previous.ClockDomain == interval.ClockDomain && previous.ClockKind == interval.ClockKind && previous.StartSeconds != nil && previous.EndSeconds != nil && *previous.EndSeconds > *previous.StartSeconds && *interval.EndSeconds > *interval.StartSeconds && *previous.StartSeconds < *interval.EndSeconds && *interval.StartSeconds < *previous.EndSeconds {
					isOverlapping = true
				}
			}
		}
		if isOverlapping {
			violations = append(violations, vertex.ID+": "+phaseOverlapReason)
		}
	}
	return violations
}
