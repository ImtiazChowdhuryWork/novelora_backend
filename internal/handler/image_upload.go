package handler

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const maxImageBytes = 5 << 20 // 5 MB

// Allowed image formats, keyed by sniffed content type.
var imageExtensionsByContentType = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// imageValidationError marks upload failures caused by the client's file
// (wrong type, too large) as opposed to server faults.
type imageValidationError struct {
	message string
}

func (validationError *imageValidationError) Error() string {
	return validationError.message
}

// saveUploadedImage stores the multipart image field as
// <directory>/<baseName>.<ext> (replacing any stale extension variants)
// and returns a cache-busted public URL under publicPrefix.
// The real content type is sniffed — the client's filename is not trusted.
func saveUploadedImage(request *http.Request, fieldName, directory, publicPrefix, baseName string) (string, error) {
	request.Body = http.MaxBytesReader(nil, request.Body, maxImageBytes)
	if err := request.ParseMultipartForm(maxImageBytes); err != nil {
		return "", &imageValidationError{"image is too large (max 5MB) or the form is invalid"}
	}

	uploadedFile, _, err := request.FormFile(fieldName)
	if err != nil {
		return "", &imageValidationError{"multipart field '" + fieldName + "' is required"}
	}
	defer uploadedFile.Close()

	sniffBuffer := make([]byte, 512)
	bytesRead, err := io.ReadFull(uploadedFile, sniffBuffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", &imageValidationError{"could not read the uploaded file"}
	}
	sniffBuffer = sniffBuffer[:bytesRead]

	extension, allowed := imageExtensionsByContentType[http.DetectContentType(sniffBuffer)]
	if !allowed {
		return "", &imageValidationError{"image must be a JPEG, PNG, or WebP"}
	}

	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	removeStaleImageFiles(directory, baseName, extension)

	destination, err := os.Create(filepath.Join(directory, baseName+extension))
	if err != nil {
		return "", err
	}
	defer destination.Close()
	if _, err := destination.Write(sniffBuffer); err != nil {
		return "", err
	}
	if _, err := io.Copy(destination, uploadedFile); err != nil {
		return "", err
	}

	return publicPrefix + baseName + extension +
		"?v=" + strconv.FormatInt(time.Now().Unix(), 10), nil
}

// writeImageUploadError maps upload failures: client faults → 400,
// everything else → 500.
func writeImageUploadError(responseWriter http.ResponseWriter, err error) {
	var validationError *imageValidationError
	if errors.As(err, &validationError) {
		writeError(responseWriter, http.StatusBadRequest, validationError.message)
		return
	}
	log.Printf("internal error: %v", err)
	writeError(responseWriter, http.StatusInternalServerError, "internal server error")
}

// removeStaleImageFiles deletes the base name's files in other
// extensions ("" keeps none) so format switches leave no orphans.
func removeStaleImageFiles(directory, baseName, keepExtension string) {
	for _, extension := range imageExtensionsByContentType {
		if extension != keepExtension {
			os.Remove(filepath.Join(directory, baseName+extension))
		}
	}
}
