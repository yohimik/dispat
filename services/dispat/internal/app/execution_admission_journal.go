package app

import "fmt"

const (
	firstRunAdmissionInertState         = "inert"
	firstRunAdmissionArmedState         = "armed"
	firstRunAdmissionJournalErrorFormat = "execution: first-node admission journal is unavailable: %w"
	firstRunAdmissionNotInert           = false
	firstRunSafeRetirementState         = true
)

// The marker reaches durable storage before any broker is exposed. Partial
// initialization can therefore retain diagnostic refs without retaining a
// release lock for a command that could never have been accepted.
func (j *firstRunJournal) armChoreographyAdmission() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	identity, err := j.read()
	if err != nil {
		return err
	}
	if identity.AdmissionState == firstRunAdmissionArmedState {
		return nil
	}
	identity.AdmissionState = firstRunAdmissionArmedState
	return j.write(identity)
}

func (a *App) isUnboundChoreographyInert() (bool, error) {
	if a.choreographyBound || a.returnJournal == nil {
		return firstRunAdmissionNotInert, nil
	}
	identity, err := a.returnJournal.read()
	if err != nil {
		return firstRunAdmissionNotInert, fmt.Errorf(firstRunAdmissionJournalErrorFormat, err)
	}
	return identity.Run == a.runID && identity.AdmissionState == firstRunAdmissionInertState, nil
}
