package mindvoice_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MindVoice-Pro/mindvoice-sdk-go"
)

type observedReader struct {
	*observedBody
	read bool
}

// Read позволяет отличить построение запроса от чтения переданного аудиопотока.
func (r *observedReader) Read(p []byte) (int, error) {
	r.read = true
	return r.observedBody.Read(p)
}

// TestUploadRecording проверяет поток файла, MIME-типы multipart и необязательные поля.
func TestUploadRecording(t *testing.T) {
	audio := &observedReader{observedBody: &observedBody{Reader: strings.NewReader("audio bytes")}}
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/proxy/v1/workspaces/ws-example/recordings" {
			t.Fatalf("неожиданный запрос %s %s", req.Method, req.URL)
		}
		if audio.read {
			t.Fatal("файл прочитан до отправки запроса")
		}
		defer req.Body.Close()
		mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			t.Fatalf("неожиданный Content-Type %q: %v", mediaType, err)
		}

		// Считываем настоящий multipart, проверяя данные и заголовки каждой части.
		fields := make(map[string]string)
		reader := multipart.NewReader(req.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			fields[part.FormName()] = string(data)
			switch part.FormName() {
			case "file":
				if part.FileName() != `Звонок "1".mp3` || part.Header.Get("Content-Type") != "audio/mpeg" ||
					!strings.Contains(part.Header.Get("Content-Disposition"), `; filename="Звонок \"1\".mp3"`) {
					t.Fatalf("имя файла %q, заголовки %v", part.FileName(), part.Header)
				}
			case "metadata":
				if part.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("заголовки metadata: %v", part.Header)
				}
			}
		}
		if len(fields) != 5 || fields["file"] != "audio bytes" || fields["check_duplicates"] != "false" ||
			fields["retention_days"] != "30" || fields["recorded_at"] != "2026-11-25T15:30:00.123+03:00" ||
			strings.TrimSpace(fields["metadata"]) != `{"number":9007199254740993,"unset":null}` {
			t.Fatalf("неожиданные поля multipart: %v", fields)
		}
		return testResponse(http.StatusOK, `{"recording":{"id":"fl-uploaded"}}`), nil
	})
	recordedAt := time.Date(2026, time.November, 25, 15, 30, 0, 123_000_000, time.FixedZone("UTC+3", 3*60*60))
	result, err := client.UploadRecording(t.Context(), "ws-example", mindvoice.UploadRecordingInput{
		File:        audio,
		Filename:    `Звонок "1".mp3`,
		ContentType: "audio/mpeg",
		Metadata: map[string]any{
			"number": json.Number("9007199254740993"),
			"unset":  nil,
		},
		RecordedAt:      &recordedAt,
		RetentionDays:   new(int32(30)),
		CheckDuplicates: new(false),
	})
	if err != nil || result.Recording.ID != "fl-uploaded" || !audio.read || audio.closed {
		t.Fatalf("результат %+v, ошибка %v, файл прочитан: %v, закрыт: %v", result, err, audio.read, audio.closed)
	}
}

// TestUploadRecordingRejectsInvalidParts проверяет ошибки до чтения файла и отправки запроса.
func TestUploadRecordingRejectsInvalidParts(t *testing.T) {
	client := newTestClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("SDK отправил некорректный запрос")
		return nil, nil
	})
	audio := &observedReader{observedBody: &observedBody{Reader: strings.NewReader("audio")}}
	for _, input := range []mindvoice.UploadRecordingInput{
		{},
		{File: audio, Filename: "invalid\r\n.mp3", ContentType: "audio/mpeg"},
		{File: audio, Filename: "call.mp3", ContentType: "audio/mpeg\r\n"},
		{File: audio, Filename: "call.mp3", ContentType: "audio/mpeg", Metadata: map[string]any{"invalid": make(chan int)}},
	} {
		if _, err := client.UploadRecording(t.Context(), "ws-example", input); err == nil || audio.read {
			t.Fatalf("ошибка %v, файл прочитан: %v", err, audio.read)
		}
	}
}

// TestDownloadRecordingRange проверяет заголовки Range и получение ещё непрочитанного тела при HTTP 206.
func TestDownloadRecordingRange(t *testing.T) {
	body := &observedReader{observedBody: &observedBody{Reader: strings.NewReader("abc")}}
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/proxy/v1/workspaces/ws-example/recordings/fl-example/download" ||
			req.Header.Get("Range") != "bytes=2-4" || req.Header.Get("If-Range") != `"tag"` {
			t.Fatalf("неожиданный запрос %s %s, заголовки %v", req.Method, req.URL, req.Header)
		}
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			ContentLength: 3,
			Header:        http.Header{"Content-Range": []string{"bytes 2-4/8"}, "Content-Type": []string{"audio/mpeg"}},
			Body:          body,
		}, nil
	})
	result, err := client.DownloadRecording(t.Context(), "ws-example", "fl-example", &mindvoice.DownloadRecordingParams{
		Range: new("bytes=2-4"), IfRange: new(`"tag"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusPartialContent || result.ContentLength != 3 ||
		result.Header.Get("Content-Range") != "bytes 2-4/8" || body.read || body.closed {
		t.Fatalf("неожиданный файловый ответ: %+v", result)
	}
	data, err := io.ReadAll(result.Body)
	if err != nil || string(data) != "abc" {
		t.Fatalf("данные %q, ошибка %v", data, err)
	}
}

// TestDownloadRecordingRangeError проверяет HTTP 416 без JSON и закрытие тела.
func TestDownloadRecordingRangeError(t *testing.T) {
	body := &observedBody{Reader: strings.NewReader("")}
	client := newTestClient(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusRequestedRangeNotSatisfiable,
			Header:     http.Header{"Content-Range": []string{"bytes */8"}},
			Body:       body,
		}, nil
	})
	result, err := client.DownloadRecording(t.Context(), "ws-example", "fl-example", nil)
	var apiErr *mindvoice.HTTPError
	if result != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusRequestedRangeNotSatisfiable ||
		apiErr.ContentRange != "bytes */8" || !body.closed {
		t.Fatalf("результат %+v, ошибка %v, тело закрыто: %v", result, err, body.closed)
	}
}

// TestExportTranscript проверяет формат экспорта и отсутствие JSON-декодирования для текстового ответа.
func TestExportTranscript(t *testing.T) {
	var calls int
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet ||
			req.URL.Path != "/proxy/v1/workspaces/ws-example/transcription-runs/tr-example/transcript/export" ||
			req.URL.Query().Get("format") != "vtt" {
			t.Fatalf("неожиданный запрос %s %s", req.Method, req.URL)
		}
		resp := testResponse(http.StatusOK, "WEBVTT\n\n")
		resp.Header.Set("Content-Type", "text/vtt; charset=utf-8")
		resp.Header.Set("Content-Disposition", `attachment; filename="transcript.vtt"`)
		return resp, nil
	})
	result, err := client.ExportTranscript(t.Context(), "ws-example", "tr-example", mindvoice.FormatVTT)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	data, err := io.ReadAll(result.Body)
	if err != nil || string(data) != "WEBVTT\n\n" ||
		result.Header.Get("Content-Disposition") != `attachment; filename="transcript.vtt"` {
		t.Fatalf("текст %q, ошибка %v, заголовки %v", data, err, result.Header)
	}
	if _, err := client.ExportTranscript(t.Context(), "ws-example", "tr-example", "invalid"); err == nil || calls != 1 {
		t.Fatalf("ошибка %v, запросов %d", err, calls)
	}
}

// ExampleClient_ListWorkspaces показывает чтение идентификатора пространства из ответа.
func ExampleClient_ListWorkspaces() {
	httpClient := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return testResponse(http.StatusOK, `{"workspaces":[{"id":"ws-example","name":"Продажи"}]}`), nil
	})}
	client, err := mindvoice.New("test-token", mindvoice.WithClient(httpClient))
	if err != nil {
		panic(err)
	}
	result, err := client.ListWorkspaces(context.Background())
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Workspaces[0].ID)
	// Output: ws-example
}
