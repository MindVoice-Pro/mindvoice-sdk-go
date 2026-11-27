package mindvoice

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxErrorBodyBytes = 64 << 10

// HTTPError содержит статус ответа, сведения API и заголовки, нужные для
// повторного запроса. При ответе без JSON поля APIError остаются пустыми.
type HTTPError struct {
	APIError
	StatusCode   int
	RetryAfter   string
	ContentRange string
}

// Error возвращает HTTP-статус и, если доступен, доменный код MindVoice.
func (e *HTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("mindvoice: HTTP %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("mindvoice: HTTP %d", e.StatusCode)
}

// jsonResponse декодирует типизированный ответ и закрывает тело ответа при любом исходе.
func jsonResponse[T any](resp *http.Response, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	if !successful(resp) {
		return nil, readHTTPError(resp)
	}
	defer resp.Body.Close()

	// Проверяем весь JSON-документ, чтобы отсечь лишние данные после первого значения.
	decoder := json.NewDecoder(resp.Body)
	var result *T
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("mindvoice: чтение ответа API: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("mindvoice: пустой JSON-ответ API")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("mindvoice: чтение конца ответа API: %w", err)
		}
		return nil, fmt.Errorf("mindvoice: в ответе API несколько JSON-значений")
	}
	return result, nil
}

// successful проверяет, вернул ли HTTP-сервер код из семейства 2xx.
func successful(resp *http.Response) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// readHTTPError читает ограниченный объём тела и сохраняет HTTP-статус даже без JSON.
func readHTTPError(resp *http.Response) *HTTPError {
	defer resp.Body.Close()
	result := &HTTPError{
		StatusCode:   resp.StatusCode,
		RetryAfter:   resp.Header.Get("Retry-After"),
		ContentRange: resp.Header.Get("Content-Range"),
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if err == nil && len(body) <= maxErrorBodyBytes {
		var envelope ErrorResponse
		if json.Unmarshal(body, &envelope) == nil {
			result.APIError = envelope.Error
		}
	}
	if result.RequestID == nil {
		if id := resp.Header.Get("X-Request-ID"); id != "" {
			result.RequestID = &id
		}
	}
	return result
}
