package app

import "github.com/yohimik/dispat/services/dispat/internal/cond"

// The condition grammar `dispat if` branches on lives in the cond package,
// shared with the webhook env gate; these aliases keep the command's callers
// reading app alone.
type Condition = cond.Condition

var (
	ParseCondition    = cond.ParseCondition
	ResolvedCondition = cond.ResolvedCondition
	FileCondition     = cond.FileCondition
)
