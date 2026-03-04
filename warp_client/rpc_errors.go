/*
 * Copyright (c) 2025 Karagatan LLC.
 * SPDX-License-Identifier: BUSL-1.1
 */

package warp_client

import (
	"errors"
)

var (
	ErrNoResponse   = errors.New("no response")
	ErrTimeoutError = errors.New("timeout error")
)
