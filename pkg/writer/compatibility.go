// SPDX-License-Identifier: MIT
// Copyright (c) 2026 yohimik

package writer

// Supported is retained for source compatibility.
//
// Deprecated: use IsSupported.
func Supported(path string) bool { return IsSupported(path) }

// SupportsLink is retained for source compatibility.
//
// Deprecated: use IsLinkSupported.
func SupportsLink(path string) bool { return IsLinkSupported(path) }

// Writer is retained for source compatibility.
//
// Deprecated: use Writerx.
type Writer = Writerx
