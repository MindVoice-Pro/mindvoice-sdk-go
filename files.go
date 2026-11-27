package mindvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// UploadRecordingInput содержит файл и необязательные поля загрузки.
// File остаётся открытым после запроса. Закрывать его должен вызывающий код.
type UploadRecordingInput struct {
	// File — источник содержимого аудиофайла.
	File io.Reader
	// Filename — имя исходного файла с расширением.
	Filename string
	// ContentType — MIME-тип аудио, например audio/mpeg или audio/wav.
	ContentType string
	// Metadata — пользовательские метаданные записи.
	Metadata map[string]any
	// RecordedAt — время начала записи.
	RecordedAt *time.Time
	// RetentionDays — срок хранения в днях.
	RetentionDays *int32
	// CheckDuplicates управляет проверкой дубликатов. nil оставляет настройку сервера.
	CheckDuplicates *bool
}

// FileResponse содержит открытое тело файлового ответа и его HTTP-метаданные.
// После чтения вызывающий код должен закрыть Body.
type FileResponse struct {
	Body          io.ReadCloser
	Header        http.Header
	StatusCode    int
	ContentLength int64
}

// UploadRecording загружает аудиофайл потоком multipart/form-data.
// Для управления временем загрузки передайте контекст со сроком или отменой.
func (c *Client) UploadRecording(
	ctx context.Context, workspaceID string, input UploadRecordingInput,
) (*RecordingItemResponse, error) {
	body, contentType, err := recordingMultipart(input)
	if err != nil {
		return nil, err
	}
	return jsonResponse[RecordingItemResponse](c.raw.UploadRecordingWithBody(ctx, workspaceID, contentType, body))
}

// DownloadRecording возвращает открытый поток исходного аудио.
// params позволяет задать Range и If-Range. HTTP 206 содержит выбранный диапазон.
func (c *Client) DownloadRecording(
	ctx context.Context, workspaceID, recordingID string, params *DownloadRecordingParams,
) (*FileResponse, error) {
	return fileResponse(c.raw.DownloadRecording(ctx, workspaceID, recordingID, params))
}

// ExportTranscript возвращает открытый поток текста расшифровки выбранного запуска.
func (c *Client) ExportTranscript(
	ctx context.Context, workspaceID, runID string, format TranscriptFormat,
) (*FileResponse, error) {
	if !format.Valid() {
		return nil, fmt.Errorf("mindvoice: укажите формат экспорта srt, vtt, txt или txt_timestamps")
	}
	return fileResponse(c.raw.ExportTranscript(ctx, workspaceID, runID, &ExportTranscriptParams{Format: format}))
}

// fileResponse передаёт открытое тело ответа вызывающему коду только при успехе.
func fileResponse(resp *http.Response, err error) (*FileResponse, error) {
	if err != nil {
		return nil, err
	}
	if !successful(resp) {
		return nil, readHTTPError(resp)
	}
	return &FileResponse{
		Body:          resp.Body,
		Header:        resp.Header,
		StatusCode:    resp.StatusCode,
		ContentLength: resp.ContentLength,
	}, nil
}

// recordingMultipart собирает служебные части multipart и оставляет файл потоковым.
func recordingMultipart(input UploadRecordingInput) (io.Reader, string, error) {
	if input.File == nil || strings.TrimSpace(input.Filename) == "" ||
		strings.ContainsAny(input.Filename, "\r\n\x00") {
		return nil, "", fmt.Errorf("mindvoice: укажите файл и допустимое имя файла")
	}
	if _, _, err := mime.ParseMediaType(input.ContentType); err != nil ||
		strings.ContainsAny(input.ContentType, "\r\n") {
		return nil, "", fmt.Errorf("mindvoice: укажите допустимый MIME-тип файла")
	}

	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	if err := writeUploadFields(writer, input); err != nil {
		return nil, "", fmt.Errorf("mindvoice: подготовка полей загрузки: %w", err)
	}

	// Файл идёт последней частью. Сохраняем отдельно байты до и после его содержимого.
	header := make(textproto.MIMEHeader)
	// Используем multipart-параметр filename, который backend читает как UTF-8.
	header.Set("Content-Disposition", multipart.FileContentDisposition("file", input.Filename))
	header.Set("Content-Type", input.ContentType)
	if _, err := writer.CreatePart(header); err != nil {
		return nil, "", fmt.Errorf("mindvoice: подготовка заголовков файла: %w", err)
	}
	prefixLength := envelope.Len()
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("mindvoice: завершение multipart: %w", err)
	}
	data := envelope.Bytes()
	body := io.MultiReader(bytes.NewReader(data[:prefixLength]), input.File, bytes.NewReader(data[prefixLength:]))
	return body, writer.FormDataContentType(), nil
}

// writeUploadFields кодирует только заданные поля и сохраняет явное значение false.
func writeUploadFields(writer *multipart.Writer, input UploadRecordingInput) error {
	if input.Metadata != nil {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="metadata"`)
		header.Set("Content-Type", "application/json")
		part, err := writer.CreatePart(header)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(part).Encode(input.Metadata); err != nil {
			return err
		}
	}
	if input.RecordedAt != nil {
		if err := writer.WriteField("recorded_at", input.RecordedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if input.RetentionDays != nil {
		if err := writer.WriteField("retention_days", strconv.FormatInt(int64(*input.RetentionDays), 10)); err != nil {
			return err
		}
	}
	if input.CheckDuplicates != nil {
		return writer.WriteField("check_duplicates", strconv.FormatBool(*input.CheckDuplicates))
	}
	return nil
}
