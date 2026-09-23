package fabric_msp

import "errors"

var ErrVerificationNotImplemented = errors.New("fabric-msp: cryptographic verification not implemented yet")

var (
	ErrProcessedTimeNotFound   = errors.New("fabric-msp: processed time not found")
	ErrProcessedHeightNotFound = errors.New("fabric-msp: processed height not found")
	ErrDelayPeriodNotPassed    = errors.New("fabric-msp: connection delay period has not passed")
)
