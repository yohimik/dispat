// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"sync"
)

const choreographyNativeInputLaneError = "execution: native input write cannot enter the compiled claim lane"

// BeginNativeInputWrite fences new Claim CASes before a local effect can
// change a prepared source. Its release must follow the relevance check and
// any signed input invalidation, while the native effect guard is still held.
func (b *FirstNodeBroker) BeginNativeInputWrite(ctx context.Context) (func(), error) {
	if b == nil || ctx == nil || ctx.Err() != nil {
		return nil, fmt.Errorf(choreographyNativeInputLaneError)
	}
	b.nativeInputLane.Lock()
	if ctx.Err() != nil {
		b.nativeInputLane.Unlock()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() { once.Do(b.nativeInputLane.Unlock) }, nil
}
