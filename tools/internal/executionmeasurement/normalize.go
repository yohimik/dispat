package executionmeasurement

func presentSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func normalizeEmptyRecords(measurement *Measurement) {
	measurement.Trial.Evidence = presentSlice(measurement.Trial.Evidence)
	measurement.Graph.Vertices = presentSlice(measurement.Graph.Vertices)
	measurement.Graph.Edges = presentSlice(measurement.Graph.Edges)
	measurement.Graph.Evidence = presentSlice(measurement.Graph.Evidence)
	measurement.Graph.OmittedWork = presentSlice(measurement.Graph.OmittedWork)
	for index := range measurement.Graph.Vertices {
		measurement.Graph.Vertices[index].DurationInputs = presentSlice(measurement.Graph.Vertices[index].DurationInputs)
	}
	measurement.Resources.Nodes = presentSlice(measurement.Resources.Nodes)
	measurement.Resources.Pools = presentSlice(measurement.Resources.Pools)
	measurement.Resources.Nested = presentSlice(measurement.Resources.Nested)
	measurement.Resources.TaskSlotLimit.Evidence = presentSlice(measurement.Resources.TaskSlotLimit.Evidence)
	measurement.Resources.UsableOnePoolSlots.Evidence = presentSlice(measurement.Resources.UsableOnePoolSlots.Evidence)
	measurement.ObservedPeakOverlap.Evidence = presentSlice(measurement.ObservedPeakOverlap.Evidence)
	measurement.TaskIntervals = presentSlice(measurement.TaskIntervals)
	for index := range measurement.TaskIntervals {
		measurement.TaskIntervals[index].Evidence = presentSlice(measurement.TaskIntervals[index].Evidence)
	}
	measurement.TimeWindows = presentSlice(measurement.TimeWindows)
	for index := range measurement.TimeWindows {
		measurement.TimeWindows[index].Evidence = presentSlice(measurement.TimeWindows[index].Evidence)
	}
}
