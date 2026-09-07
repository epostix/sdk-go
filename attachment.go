package epostix

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	AttachmentDecodedLimitBytes = 41943040
	RequestBodyLimitBytes       = 52428800
)

type AttachmentTooLargeError struct {
	Filename    string
	ActualBytes int
	LimitBytes  int
}

func (e *AttachmentTooLargeError) Error() string {
	return fmt.Sprintf("epostix: attachment %q is %d bytes, above the %d byte decoded limit",
		e.Filename, e.ActualBytes, e.LimitBytes)
}

type RequestTooLargeError struct {
	ProjectedEncodedBytes int
	LimitBytes            int
	AttachmentCount       int
}

func (e *RequestTooLargeError) Error() string {
	return fmt.Sprintf(
		"epostix: %d attachment(s) encode to about %d bytes, above the %d byte request limit; "+
			"upload them first, or pass a content URL",
		e.AttachmentCount, e.ProjectedEncodedBytes, e.LimitBytes)
}

func ProjectEncodedSize(decodedBytes int) int {
	return ((decodedBytes + 2) / 3) * 4
}

func checkAttachmentSize(filename string, size int) error {
	if size > AttachmentDecodedLimitBytes {
		return &AttachmentTooLargeError{
			Filename: filename, ActualBytes: size, LimitBytes: AttachmentDecodedLimitBytes,
		}
	}

	if projected := ProjectEncodedSize(size); projected > RequestBodyLimitBytes {
		return &RequestTooLargeError{
			ProjectedEncodedBytes: projected, LimitBytes: RequestBodyLimitBytes, AttachmentCount: 1,
		}
	}

	return nil
}

func AttachmentFromBytes(filename string, content []byte, contentType string) (Attachment, error) {
	if err := checkAttachmentSize(filename, len(content)); err != nil {
		return Attachment{}, err
	}

	return Attachment{
		Filename:    filename,
		Content:     base64.StdEncoding.EncodeToString(content),
		ContentType: contentType,
	}, nil
}

func AttachmentFromFile(path string, contentType string) (Attachment, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Attachment{}, fmt.Errorf("epostix: read attachment: %w", err)
	}

	return AttachmentFromBytes(filepath.Base(path), content, contentType)
}

func AttachmentFromReader(filename string, reader io.Reader, contentType string) (Attachment, error) {
	content, err := io.ReadAll(io.LimitReader(reader, AttachmentDecodedLimitBytes+1))
	if err != nil {
		return Attachment{}, fmt.Errorf("epostix: read attachment: %w", err)
	}

	return AttachmentFromBytes(filename, content, contentType)
}

func AttachmentFromUploadedID(attachmentID string) AttachmentRef {
	return AttachmentRef{AttachmentID: attachmentID}
}

func AttachmentFromRemoteURL(filename, contentURL string) AttachmentURL {
	return AttachmentURL{Filename: filename, ContentURL: contentURL}
}

func AssertAttachmentsWithinLimits(sizes map[string]int) error {
	total := 0

	for filename, size := range sizes {
		if size > AttachmentDecodedLimitBytes {
			return &AttachmentTooLargeError{
				Filename: filename, ActualBytes: size, LimitBytes: AttachmentDecodedLimitBytes,
			}
		}

		total += size
	}

	if projected := ProjectEncodedSize(total); projected > RequestBodyLimitBytes {
		return &RequestTooLargeError{
			ProjectedEncodedBytes: projected, LimitBytes: RequestBodyLimitBytes, AttachmentCount: len(sizes),
		}
	}

	return nil
}
