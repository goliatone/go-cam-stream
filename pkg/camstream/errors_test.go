package camstream_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	gerrors "github.com/goliatone/go-errors"
)

func TestErrorsPreserveCauseAndCleanupClassification(t *testing.T) {
	cause := context.DeadlineExceeded
	err := camstream.NewError(camstream.CodeCleanupIncomplete, cause)
	if !errors.Is(err, cause) {
		t.Fatal("lost cancellation cause")
	}
	var structured *gerrors.Error
	if !errors.As(err, &structured) || structured.Category != gerrors.CategoryOperation {
		t.Fatal("lost structured error")
	}
	info := camstream.DescribeError(fmt.Errorf("adapter failed: %w", err))
	if info.Code != camstream.CodeCleanupIncomplete || info.Retryable {
		t.Fatalf("cleanup must retain reservation: %+v", info)
	}
}

func TestClassificationDoesNotExposeProviderData(t *testing.T) {
	for _, err := range []error{
		errors.New("rtsp://secret:password@camera"),
		gerrors.New("raw provider password", gerrors.CategoryExternal).WithTextCode("vendor_unknown"),
		camstream.NewError("not-a-contract-code", nil),
	} {
		if info := camstream.DescribeError(err); info.Code != camstream.CodeInternal || info.Message != "An internal error occurred." {
			t.Fatalf("unsafe classification: %+v", info)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if camstream.DescribeError(err).Code == camstream.CodeInternal {
			t.Fatal("context classification lost")
		}
	}
	if camstream.DescribeError(nil).Code != "" {
		t.Fatal("nil is not failure")
	}
}
