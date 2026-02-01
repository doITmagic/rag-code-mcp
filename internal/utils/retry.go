package utils

import (
	"fmt"
	"time"
)

// Retry executes a function with exponential backoff retry
func Retry(maxAttempts int, initialDelay time.Duration, fn func() error) error {
	var err error
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		if attempt < maxAttempts {
			time.Sleep(delay)
			delay *= 2 // Exponential backoff
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxAttempts, err)
}

// RetryWithContext executes a function with retry and context support
func RetryWithContext(maxAttempts int, initialDelay time.Duration, fn func() error, shouldRetry func(error) bool) error {
	var err error
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		// Check if we should retry this error
		if shouldRetry != nil && !shouldRetry(err) {
			return err
		}

		if attempt < maxAttempts {
			time.Sleep(delay)
			delay *= 2 // Exponential backoff
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxAttempts, err)
}
