package mindvoice_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MindVoice-Pro/mindvoice-sdk-go"
)

// testTransport подменяет отправку HTTP-запросов, чтобы проверять клиент без подключения к сети.
type testTransport func(*http.Request) (*http.Response, error)

// RoundTrip передаёт запрос обработчику тестового сценария.
func (fn testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type observedBody struct {
	io.Reader
	closed bool
}

// Close отмечает закрытие тела запроса или ответа.
func (b *observedBody) Close() error {
	b.closed = true
	return nil
}

// newTestClient создаёт SDK с локальным адресом и подменённым транспортом.
func newTestClient(t *testing.T, transport testTransport) *mindvoice.Client {
	t.Helper()
	c, err := mindvoice.New("test-token", mindvoice.WithBaseURL("https://mindvoice.example/proxy"),
		mindvoice.WithClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testResponse собирает HTTP-ответ с указанным содержимым.
func testResponse(code int, content string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(content)),
	}
}

// TestNewRejectsInvalidConfig проверяет ключ и базовый URL до отправки HTTP-запроса.
func TestNewRejectsInvalidConfig(t *testing.T) {
	for _, key := range []string{"", "Bearer test-token", "bad\r\nHeader"} {
		if _, err := mindvoice.New(key); err == nil {
			t.Errorf("клиент принял некорректный ключ")
		}
	}
	for _, address := range []string{
		"", "example.com", "https://", "ftp://example.com",
		"https://u:p@example.com", "https://example.com?", "https://example.com#",
	} {
		if _, err := mindvoice.New("test-token", mindvoice.WithBaseURL(address)); err == nil {
			t.Errorf("клиент принял некорректный адрес %q", address)
		}
	}
	if _, err := mindvoice.New("test-token", mindvoice.WithClient(nil)); err == nil {
		t.Error("клиент принял nil вместо HTTP-клиента")
	}
}

// TestListRecordings проверяет Bearer, префикс адреса, повторяемые параметры и точные числа.
func TestListRecordings(t *testing.T) {
	body := &observedBody{Reader: strings.NewReader(`{
		"recordings": [{"id":"fl-example","file":{"size_bytes":"18446744073709551615"},
		"metadata":{"large":9007199254740993,"unset":null}}],
		"pagination":{"total_count":1,"offset":0,"page_size":10}
	}`)}
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/proxy/v1/workspaces/ws-example/recordings" {
			t.Fatalf("неожиданный запрос %s %s", req.Method, req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("неожиданная авторизация %q", got)
		}

		// Сравниваем значения после декодирования URL и проверяем JSON-фильтр отдельно.
		q := req.URL.Query()
		if q.Get("page_size") != "10" || q.Get("offset") != "0" || q.Get("search") != "Иван & + /" ||
			q.Get("uploaded_from") != "2026-11-25" || !slices.Equal(q["status"], []string{"ready", "failed"}) {
			t.Fatalf("неожиданные параметры %v", q)
		}
		var filters []mindvoice.MetadataFilter
		if err := json.Unmarshal([]byte(q.Get("metadata_filters")), &filters); err != nil ||
			len(filters) != 1 || filters[0].Field != "branch_id" {
			t.Fatalf("неожиданный JSON-фильтр %q (%v)", q.Get("metadata_filters"), err)
		}
		resp := testResponse(http.StatusOK, "")
		resp.Body = body
		return resp, nil
	})

	pageSize, offset := int32(10), int32(0)
	search, date := "Иван & + /", "2026-11-25"
	status := []string{"ready", "failed"}
	value := json.RawMessage(`"BR-102"`)
	filters := []mindvoice.MetadataFilter{{Field: "branch_id", Operator: "equals", Value: &value}}
	result, err := client.ListRecordings(t.Context(), "ws-example", &mindvoice.ListRecordingsParams{
		PageSize: &pageSize, Offset: &offset, Search: &search, UploadedFrom: &date, Status: &status, MetadataFilters: &filters,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !body.closed || result.Pagination.TotalCount == nil || *result.Pagination.TotalCount != 1 || len(result.Recordings) != 1 {
		t.Fatalf("неожиданный ответ %+v, тело закрыто: %v", result, body.closed)
	}
	recording := result.Recordings[0]
	if recording.File.SizeBytes == nil || *recording.File.SizeBytes != "18446744073709551615" ||
		recording.Metadata == nil || string(recording.Metadata["large"]) != "9007199254740993" ||
		string(recording.Metadata["unset"]) != "null" {
		t.Fatalf("SDK изменил значения произвольного JSON или uint64: %+v", recording)
	}
}

// TestImportRecording проверяет JSON-запрос, явный false и типизированный ответ при HTTP 202.
func TestImportRecording(t *testing.T) {
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/proxy/v1/workspaces/ws-example/recordings/import" ||
			req.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("неожиданный запрос %s %s (%v)", req.Method, req.URL, req.Header)
		}

		// Проверяем фактические байты запроса и наличие поля со значением false.
		var payload map[string]json.RawMessage
		defer req.Body.Close()
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if string(payload["file"]) != `{"url":"https://storage.example/sample.mp3"}` ||
			string(payload["check_duplicates"]) != "false" {
			t.Fatalf("неожиданное тело запроса: %s", payload)
		}
		return testResponse(http.StatusAccepted, `{"recording":{"id":"fl-imported"}}`), nil
	})
	checkDuplicates := false
	result, err := client.ImportRecording(t.Context(), "ws-example", mindvoice.RecordingImport{
		File: mindvoice.RecordingImportFile{URL: "https://storage.example/sample.mp3"}, CheckDuplicates: &checkDuplicates,
	})
	if err != nil || result.Recording.ID != "fl-imported" {
		t.Fatalf("результат: %+v, ошибка: %v", result, err)
	}
}

// TestHTTPError проверяет тип ошибки, нарушения полей и подсказку о повторе.
func TestHTTPError(t *testing.T) {
	client := newTestClient(t, func(*http.Request) (*http.Response, error) {
		resp := testResponse(http.StatusTooManyRequests, `{"error":{
			"code":"rate_limited","message":"Повторите позже",
			"violations":[{"field_path":"file","code":"example","message":"Ошибка"}],
			"request_id":"req-body","details":{"retry_after_seconds":5}
		}}`)
		resp.Header.Set("Retry-After", "5")
		resp.Header.Set("X-Request-ID", "req-header")
		return resp, nil
	})

	_, err := client.ListWorkspaces(t.Context())
	var apiErr *mindvoice.HTTPError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests ||
		apiErr.Code != "rate_limited" || apiErr.RetryAfter != "5" || apiErr.Message != "Повторите позже" ||
		apiErr.RequestID == nil || *apiErr.RequestID != "req-body" ||
		len(apiErr.Violations) != 1 || apiErr.Violations[0].FieldPath != "file" {
		t.Fatalf("неожиданная ошибка: %+v", err)
	}
}

// TestClientRejectsRedirect проверяет, что SDK не передаёт ключ другому адресу.
func TestClientRejectsRedirect(t *testing.T) {
	var calls int
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Host != "mindvoice.example" {
			t.Fatal("SDK последовал по перенаправлению")
		}
		resp := testResponse(http.StatusTemporaryRedirect, "<html>перенаправление</html>")
		resp.Header.Set("Location", "https://other.example/file")
		resp.Header.Set("X-Request-ID", "req-proxy")
		return resp, nil
	})
	_, err := client.ListWorkspaces(t.Context())
	var apiErr *mindvoice.HTTPError
	if !errors.As(err, &apiErr) || calls != 1 || apiErr.StatusCode != http.StatusTemporaryRedirect ||
		apiErr.RequestID == nil || *apiErr.RequestID != "req-proxy" {
		t.Fatalf("перенаправление: вызовов %d, ошибка %v", calls, err)
	}
}

// TestListWorkspacesRejectsMalformedSuccess проверяет ошибки декодирования JSON и закрытие тела.
func TestListWorkspacesRejectsMalformedSuccess(t *testing.T) {
	for _, content := range []string{"", "null", "{", `{"workspaces":[]} {}`} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			body := &observedBody{Reader: strings.NewReader(content)}
			client := newTestClient(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			if _, err := client.ListWorkspaces(t.Context()); err == nil || !body.closed {
				t.Fatalf("ошибка %v, тело закрыто: %v", err, body.closed)
			}
		})
	}
}

// TestClientRespectsContext проверяет передачу отмены из HTTP-клиента.
func TestClientRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})
	if _, err := client.ListWorkspaces(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась отмена запроса, получено: %v", err)
	}
}

// TestAnswerValuePresence проверяет, что false и пустая строка отличаются от отсутствующего поля.
func TestAnswerValuePresence(t *testing.T) {
	for _, example := range []struct {
		json string
		want mindvoice.AnswerValue
	}{
		{json: `{"boolean_value":false}`, want: mindvoice.AnswerValue{BooleanValue: new(false)}},
		{json: `{"text_value":""}`, want: mindvoice.AnswerValue{TextValue: new("")}},
	} {
		var got mindvoice.AnswerValue
		if err := json.Unmarshal([]byte(example.json), &got); err != nil || !reflect.DeepEqual(got, example.want) {
			t.Fatalf("декодирование %s: %+v, %v", example.json, got, err)
		}
		encoded, err := json.Marshal(got)
		if err != nil || string(encoded) != example.json {
			t.Fatalf("кодирование %s: %s, %v", example.json, encoded, err)
		}
	}
}
