// Package mindvoice предоставляет Go-клиент публичного API MindVoice с ключом Workspace.
package mindvoice

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// DefaultBaseURL — адрес публичного API MindVoice.
const DefaultBaseURL = "https://api.mindvoice.pro"

// Client вызывает операции публичного API MindVoice. Клиент можно использовать
// одновременно из нескольких горутин, если внедрённый транспорт поддерживает это.
type Client struct {
	raw *RawClient
}

type clientOptions struct {
	baseURL    string
	httpClient *http.Client
}

// Option задаёт настройку клиента при создании.
type Option func(*clientOptions)

// New создаёт клиент для ключа Workspace. Создание клиента не отправляет HTTP-запрос.
// Операции используют переданный context, в том числе при чтении файлового ответа.
func New(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" || strings.IndexFunc(apiKey, func(r rune) bool {
		// Токен Bearer должен состоять из печатных ASCII-символов без пробелов.
		return r < '!' || r > '~'
	}) >= 0 {
		return nil, errors.New("mindvoice: укажите допустимый API-ключ Workspace")
	}

	cfg := clientOptions{baseURL: DefaultBaseURL, httpClient: http.DefaultClient}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("mindvoice: настройка клиента не задана")
		}
		opt(&cfg)
	}
	if cfg.httpClient == nil {
		return nil, errors.New("mindvoice: HTTP-клиент не задан")
	}
	baseURL, err := url.Parse(cfg.baseURL)
	if err != nil || baseURL.Hostname() == "" || baseURL.User != nil ||
		baseURL.ForceQuery || baseURL.RawQuery != "" || strings.Contains(cfg.baseURL, "#") ||
		(baseURL.Scheme != "https" && baseURL.Scheme != "http") {
		return nil, errors.New("mindvoice: базовый адрес должен быть HTTP(S) URL без логина, query и фрагмента")
	}

	// Копируем настройки HTTP-клиента и оставляем ключ на исходном адресе при редиректе.
	httpClient := *cfg.httpClient
	httpClient.CheckRedirect = rejectRedirect
	raw, err := NewClient(baseURL.String(), WithHTTPClient(&httpClient), WithRequestEditorFn(bearerAuth(apiKey)))
	if err != nil {
		return nil, err
	}
	return &Client{raw: raw}, nil
}

// WithBaseURL задаёт адрес API с необязательным префиксом пути для прокси или тестов.
func WithBaseURL(baseURL string) Option {
	return func(opts *clientOptions) {
		opts.baseURL = baseURL
	}
}

// WithClient задаёт настройки HTTP-клиента. SDK копирует их и отключает
// перенаправления, чтобы API-ключ передавался только на исходный адрес.
func WithClient(client *http.Client) Option {
	return func(opts *clientOptions) {
		opts.httpClient = client
	}
}

// rejectRedirect оставляет ответ 3xx для обработки наравне с остальными ошибками API.
func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// bearerAuth добавляет ключ Workspace к исходящему запросу API.
func bearerAuth(apiKey string) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		return nil
	}
}
