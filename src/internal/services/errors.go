package services

import (
	"errors"
	"fmt"
)

// TaskNotFoundError is returned when a task ID does not exist.
type TaskNotFoundError struct {
	ID string
}

func (e *TaskNotFoundError) Error() string {
	return fmt.Sprintf("task with ID %s not found", e.ID)
}

// ValidationError is returned when input fails validation.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// IsNotFound reports whether err is a TaskNotFoundError.
func IsNotFound(err error) bool {
	var nf *TaskNotFoundError
	return errors.As(err, &nf)
}

// IsValidation reports whether err is a ValidationError.
func IsValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
