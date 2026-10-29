package executionmeasurement

type schedulePremise string

const (
	scheduleEligible schedulePremise = "eligible"
	scheduleMissing  schedulePremise = "missing"
	scheduleViolated schedulePremise = "violated"
	failedStatus                     = "failed"
	timeoutStatus                    = "timeout"
)

func classifySchedulePremise(measurement *Measurement, areDurationsComplete bool) schedulePremise {
	if measurement.Trial.Status == failedStatus || measurement.Trial.Status == timeoutStatus {
		return scheduleViolated
	}
	premise := scheduleMissing
	if measurement.Trial.Status == completedStatus && areDurationsComplete {
		premise = scheduleEligible
	}
	attempts := map[string]string{}
	for _, interval := range measurement.TaskIntervals {
		if interval.Status == failedStatus || interval.Status == timeoutStatus {
			return scheduleViolated
		}
		if previous, exists := attempts[interval.TaskID]; exists && previous != interval.AttemptID {
			return scheduleViolated
		}
		attempts[interval.TaskID] = interval.AttemptID
		if interval.Status != completedStatus && interval.Status != successfulStatus {
			premise = scheduleMissing
		}
	}
	return premise
}
