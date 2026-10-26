// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

const (
	runReturnAbsentCertificatePinText         = ""
	runReturnSerialBaseValue                  = 1
	runReturnSerialSizeBits                   = 128
	runReturnCertificateCommonName            = "dispat return channel"
	runReturnCertificateClockSkewMinutes      = 1
	runReturnCertificateLifetimeYears         = 1
	runReturnCertificateExtraMonths           = 0
	runReturnCertificateExtraDays             = 0
	runReturnCertificateValidConstraintsState = true
	runReturnNetworkName                      = "tcp"
	runReturnReadHeaderTimeoutSeconds         = 10
	runReturnMaximumHeaderBytes               = 64 << 10
	runReturnCompletionBufferCount            = 1
	runReturnNotReadyErrorText                = "run return channel is not ready"
)
