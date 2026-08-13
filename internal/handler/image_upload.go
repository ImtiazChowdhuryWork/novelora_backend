package handler

import (
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const maxImageBytes = 5 << 20 // 5 MB
const maxReportImages = 3

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

// sniffImageExtension identifies an uploaded file's real format from
// its content (never trusts the client's filename) and returns the
// matching extension plus the sniff buffer, which callers must write
// first before copying the rest of the file — reading it consumed the
// stream's first bytes.
func sniffImageExtension(file multipart.File) (extension string, sniffBuffer []byte, err error) {
	sniffBuffer = make([]byte, 512)
	bytesRead, err := io.ReadFull(file, sniffBuffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", nil, &imageValidationError{"could not read the uploaded file"}
	}
	sniffBuffer = sniffBuffer[:bytesRead]

	extension, allowed := imageExtensionsByContentType[http.DetectContentType(sniffBuffer)]
	if !allowed {
		return "", nil, &imageValidationError{"image must be a JPEG, PNG, or WebP"}
	}
	return extension, sniffBuffer, nil
}

// saveUploadedImage stores the multipart image field as
// <directory>/<baseName>.<ext> (replacing any stale extension variants)
// and returns a cache-busted public URL under publicPrefix.
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

	extension, sniffBuffer, err := sniffImageExtension(uploadedFile)
	if err != nil {
		return "", err
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

// saveUploadedReportImages stores up to maxReportImages files from a
// multipart field allowing multiple values, each as
// <directory>/<baseName>_<index>.<ext>, returning their public URLs in
// submission order. Unlike saveUploadedImage, there's no stale-file
// cleanup — every report's images are new files, never a slot being
// replaced. An empty field (no evidence attached) is not an error.
func saveUploadedReportImages(request *http.Request, fieldName, directory, publicPrefix, baseName string) ([]string, error) {
	request.Body = http.MaxBytesReader(nil, request.Body, maxImageBytes*maxReportImages)
	if err := request.ParseMultipartForm(maxImageBytes * maxReportImages); err != nil {
		return nil, &imageValidationError{"images are too large (max 5MB each) or the form is invalid"}
	}
	if request.MultipartForm == nil {
		return nil, nil
	}

	files := request.MultipartForm.File[fieldName]
	if len(files) > maxReportImages {
		return nil, &imageValidationError{fmt.Sprintf("at most %d images allowed", maxReportImages)}
	}
	if len(files) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(files))
	for index, fileHeader := range files {
		url, err := saveOneReportImage(fileHeader, directory, publicPrefix, baseName, index)
		if err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, nil
}

func saveOneReportImage(fileHeader *multipart.FileHeader, directory, publicPrefix, baseName string, index int) (string, error) {
	uploadedFile, err := fileHeader.Open()
	if err != nil {
		return "", &imageValidationError{"could not read the uploaded file"}
	}
	defer uploadedFile.Close()

	extension, sniffBuffer, err := sniffImageExtension(uploadedFile)
	if err != nil {
		return "", err
	}

	imageName := fmt.Sprintf("%s_%d%s", baseName, index, extension)
	destination, err := os.Create(filepath.Join(directory, imageName))
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

	return publicPrefix + imageName + "?v=" + strconv.FormatInt(time.Now().Unix(), 10), nil
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
