package epostix

type BatchOutcomeStatus string

const (
	OutcomeAccepted   BatchOutcomeStatus = "accepted"
	OutcomeRejected   BatchOutcomeStatus = "rejected"
	OutcomeUnresolved BatchOutcomeStatus = "unresolved"
)

type UnresolvedReason string

const (
	UnresolvedNotReported              UnresolvedReason = "NotReported"
	UnresolvedStatusFailedWithoutError UnresolvedReason = "StatusFailedWithoutError"
)

type BatchOutcome struct {
	Index     int
	Status    BatchOutcomeStatus
	EmailID   string
	SendState EmailBatchResultStatus
	ErrorType APIErrorType
	Message   string
	Reason    UnresolvedReason
}

type BatchResult struct {
	BatchID          string
	Outcomes         []BatchOutcome
	Accepted         []BatchOutcome
	Rejected         []BatchOutcome
	Unresolved       []BatchOutcome
	AcceptedEmailIDs []string
	AllAccepted      bool
	HasUnresolved    bool
	Raw              *EmailBatchResponse
	Meta             ResponseMeta
}

func NormalizeBatch(response *EmailBatchResponse, inputCount int) BatchResult {
	byIndex := make(map[int]BatchOutcome, inputCount)

	if response != nil {
		for _, item := range response.Results {
			if item.Index < 0 {
				continue
			}

			if item.ID != nil && *item.ID != "" && item.Status != EmailBatchResultStatusFailed {
				byIndex[item.Index] = BatchOutcome{
					Index: item.Index, Status: OutcomeAccepted,
					EmailID: *item.ID, SendState: item.Status,
				}

				continue
			}

			if item.Error != nil {
				byIndex[item.Index] = BatchOutcome{
					Index: item.Index, Status: OutcomeRejected,
					ErrorType: item.Error.Type, Message: item.Error.Message,
				}

				continue
			}

			byIndex[item.Index] = BatchOutcome{
				Index: item.Index, Status: OutcomeUnresolved,
				Reason: UnresolvedStatusFailedWithoutError,
			}
		}

		for _, failure := range response.Errors {
			if _, seen := byIndex[failure.Index]; seen || failure.Index < 0 {
				continue
			}

			byIndex[failure.Index] = BatchOutcome{
				Index: failure.Index, Status: OutcomeRejected,
				ErrorType: APIErrorType(failure.Code), Message: failure.Message,
			}
		}
	}

	result := BatchResult{Outcomes: make([]BatchOutcome, 0, inputCount), Raw: response}

	if response != nil {
		result.BatchID = response.BatchID
		result.Meta = response.Meta
	}

	for index := 0; index < inputCount; index++ {
		outcome, reported := byIndex[index]
		if !reported {
			outcome = BatchOutcome{Index: index, Status: OutcomeUnresolved, Reason: UnresolvedNotReported}
		}

		result.Outcomes = append(result.Outcomes, outcome)

		switch outcome.Status {
		case OutcomeAccepted:
			result.Accepted = append(result.Accepted, outcome)
			result.AcceptedEmailIDs = append(result.AcceptedEmailIDs, outcome.EmailID)
		case OutcomeRejected:
			result.Rejected = append(result.Rejected, outcome)
		case OutcomeUnresolved:
			result.Unresolved = append(result.Unresolved, outcome)
		}
	}

	result.AllAccepted = len(result.Rejected) == 0 && len(result.Unresolved) == 0
	result.HasUnresolved = len(result.Unresolved) > 0

	return result
}
