package iam

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/api/googleapi"
)

const (
	iamPropagationTimeout        = 120 * time.Second
	iamPropagationInitialBackoff = 2 * time.Second
	iamPropagationMaxBackoff     = 16 * time.Second
)

// retryWithBackoff retries an operation with exponential backoff,
// handling transient errors caused by IAM eventual consistency.
func retryWithBackoff(ctx context.Context, logger logr.Logger, operationName string, operation func() error) error {
	deadline := time.Now().Add(iamPropagationTimeout)
	backoff := iamPropagationInitialBackoff
	attempt := 0

	for {
		attempt++
		err := operation()

		if err == nil {
			if attempt > 1 {
				logger.Info("Operation succeeded after retry", "operation", operationName, "attempts", attempt)
			}
			return nil
		}

		if !isTransientIAMError(err) {
			logger.Info("Operation failed with non-transient error", "operation", operationName, "error", err)
			return err
		}

		if time.Now().After(deadline) {
			logger.Info("Operation timed out after retries", "operation", operationName, "attempts", attempt, "lastError", err)
			return fmt.Errorf("operation timed out after %d attempts due to IAM propagation delays: %w", attempt, err)
		}

		if ctx.Err() != nil {
			return fmt.Errorf("operation canceled: %w", ctx.Err())
		}

		jitter := time.Duration(float64(backoff) * (0.75 + rand.Float64()*0.5))

		logger.Info("Retrying operation due to IAM propagation delay",
			"operation", operationName,
			"attempt", attempt,
			"backoff", jitter,
			"error", err.Error())

		select {
		case <-time.After(jitter):
		case <-ctx.Done():
			return fmt.Errorf("operation canceled during backoff: %w", ctx.Err())
		}

		backoff *= 2
		if backoff > iamPropagationMaxBackoff {
			backoff = iamPropagationMaxBackoff
		}
	}
}

// isTransientIAMError checks if the error is likely due to IAM eventual consistency.
// These errors should be retried as they typically resolve once IAM changes propagate.
func isTransientIAMError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.Code {
	case 404:
		return true
	case 429:
		return true
	case 400:
		return containsAny(apiErr.Message, "IAM", "permission", "policy", "does not exist", "Service account")
	case 403:
		return containsAny(apiErr.Message, "Permission", "policy")
	default:
		return false
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 404
	}
	return false
}

func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 409
	}
	return false
}
