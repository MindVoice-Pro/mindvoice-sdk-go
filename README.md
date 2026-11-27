# MindVoice SDK для Go

Официальный клиент публичного API MindVoice для серверных приложений на Go.
Через SDK можно загрузить или импортировать аудио, запустить настроенные
сценарии, получить текст расшифровки и результаты анализа. Клиент использует
API-ключ Workspace и по умолчанию обращается к `https://api.mindvoice.pro`.

[Портал разработчиков MindVoice](https://developer.mindvoice.pro/) ·
[Справочник API](https://developer.mindvoice.pro/api-reference/)

## Установка

Требуется **Go 1.26 или новее**. В каталоге вашего Go-модуля выполните:

```bash
go get github.com/MindVoice-Pro/mindvoice-sdk-go
```

Если вы проверяете SDK в пустом каталоге, сначала выполните
`go mod init example.com/mindvoice-quickstart`. Пакет импортируется под именем
`mindvoice`.

## API-ключ и права доступа

В MindVoice выберите нужное рабочее пространство и откройте **Настройки → Workspace → API-ключи**.
Создайте ключ с правами вашей интеграции. Скопируйте секрет вида `ws-sk-…` при
создании и передайте его серверному приложению через переменную
`MINDVOICE_API_KEY`. Храните секрет в менеджере секретов и держите его вне
клиентских приложений, репозитория и логов. Для каждого Workspace используется
свой ключ. SDK сам формирует заголовок `Authorization: Bearer …`.
[Получение API-ключа](https://developer.mindvoice.pro/docs/getting-started/authentication/)

| Действие | Право ключа |
| --- | --- |
| Загрузка аудио и импорт по ссылке | `recordings:write` |
| Список записей, статус подготовки и скачивание исходного аудио | `recordings:read` |
| Ручной запуск транскрибации | `transcripts:write` |
| Чтение сценариев и запусков транскрибации, получение и экспорт текста расшифровки | `transcripts:read` |
| Ручной запуск анализа | `analysis:write` |
| Чтение сценариев и запусков анализа, получение результатов | `analysis:read` |

Если сценарии запускаются автоматически, для получения их результатов
достаточно соответствующих прав чтения. Сценарии создаются и настраиваются
в интерфейсе MindVoice.
[Сценарии транскрибации и анализа](https://developer.mindvoice.pro/docs/guides/scenarios/)

## Первый запрос

Сохраните пример в `main.go`. Для локального запуска из Bash введите ключ
без отображения на экране и запустите программу.

```bash
read -r -s -p 'API-ключ MindVoice: ' MINDVOICE_API_KEY
printf '\n'
export MINDVOICE_API_KEY
go run .
```

Программа выведет ID и название рабочего пространства, доступного ключу.
Используйте этот ID как `workspaceID` в следующих примерах.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/MindVoice-Pro/mindvoice-sdk-go"
)

// main проверяет доступ и выводит идентификаторы доступных рабочих пространств.
func main() {
	apiKey := os.Getenv("MINDVOICE_API_KEY")
	if apiKey == "" {
		log.Fatal("задайте MINDVOICE_API_KEY")
	}

	client, err := mindvoice.New(apiKey)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := client.ListWorkspaces(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if len(result.Workspaces) == 0 {
		log.Fatal("ключу недоступны рабочие пространства")
	}
	for _, workspace := range result.Workspaces {
		fmt.Printf("%s\t%s\n", workspace.ID, workspace.Name)
	}
}
```

Переиспользуйте созданный `*mindvoice.Client` для запросов с одним ключом.
`mindvoice.WithBaseURL` позволяет указать адрес тестовой среды или доверенного
прокси. `mindvoice.WithClient` принимает настроенный `*http.Client`. SDK
возвращает HTTP-перенаправления как ошибки. Через `context.Context` задавайте
срок операции, включая чтение тела при скачивании.

## Загрузка и импорт аудио

Для локального файла вызовите `UploadRecording`. Передайте имя с расширением и
MIME-тип, соответствующий формату файла. Например, для MP3 используйте
`audio/mpeg`, для WAV — `audio/wav`. SDK отправляет аудио потоком. Исходный
файл закрывает вызывающий код.

```go
// uploadMP3 загружает локальный MP3-файл и возвращает идентификатор записи.
func uploadMP3(ctx context.Context, client *mindvoice.Client, workspaceID, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	uploaded, err := client.UploadRecording(ctx, workspaceID, mindvoice.UploadRecordingInput{
		File:        file,
		Filename:    filepath.Base(path),
		ContentType: "audio/mpeg",
		Metadata:    map[string]any{"external_id": "call-123"},
	})
	if err != nil {
		return "", err
	}
	return uploaded.Recording.ID, nil
}
```

Для этого фрагмента добавьте импорт `path/filepath`. В `UploadRecordingInput`
можно также указать `RecordedAt` (`*time.Time`), `RetentionDays` (`*int32`)
и `CheckDuplicates` (`*bool`). По умолчанию API проверяет дубликаты.
Ключи верхнего уровня `Metadata` должны соответствовать `[a-z0-9_]{1,64}`.
[Форматы аудио](https://developer.mindvoice.pro/docs/guides/upload-and-results/#1-загрузите-один-или-несколько-файлов) ·
[Правила метаданных](https://developer.mindvoice.pro/docs/guides/upload-and-results/#metadata-field-names)

Если файл уже размещён в хранилище, передайте в `ImportRecording` прямую ссылку
на аудио, доступную MindVoice без дополнительных заголовков авторизации и cookie.
Метод возвращает ID записи после постановки импорта в очередь.

```go
// importRecording ставит загрузку аудио по ссылке в очередь и возвращает ID записи.
func importRecording(ctx context.Context, client *mindvoice.Client, workspaceID, audioURL string) (string, error) {
	imported, err := client.ImportRecording(ctx, workspaceID, mindvoice.RecordingImport{
		File: mindvoice.RecordingImportFile{URL: audioURL},
	})
	if err != nil {
		return "", err
	}
	return imported.Recording.ID, nil
}
```

После загрузки или импорта сохраните `recordingID`. Проверяйте
`client.GetRecording(ctx, workspaceID, recordingID)` с паузой между запросами.
`Recording.Processing.Status` со значением `completed` означает, что аудио
подготовлено и можно запускать ручную транскрибацию. При `failed`,
`download_failed` или `duplicate` проверьте карточку записи и состояние импорта.
[От файла до результата](https://developer.mindvoice.pro/docs/guides/upload-and-results/)

## Транскрибация и анализ

При включённом автоматическом сценарии используйте `ListTranscriptionRuns`
или `ListAnalysisRuns` с фильтром `RecordingID`, чтобы найти запуск для записи.

Для ручной транскрибации вызовите `client.ListTranscriptionScenarios(ctx, workspaceID, nil)`
и выберите ID готового сценария (`sct-…`) из `Scenarios`. После подготовки аудио вызовите
`StartTranscriptionRun` и сохраните `Run.ID` (`trn-…`).

```go
// startTranscription запускает транскрибацию подготовленного аудио.
func startTranscription(
	ctx context.Context, client *mindvoice.Client, workspaceID, recordingID, scenarioID string,
) (string, error) {
	started, err := client.StartTranscriptionRun(ctx, workspaceID, recordingID, mindvoice.TranscriptionRunCreate{
		TranscriptionScenarioID: scenarioID,
	})
	if err != nil {
		return "", err
	}
	return started.Run.ID, nil
}
```

Следующий пример читает статус запуска до завершения и затем получает текст
именно этого запуска. Передайте контекст с общим сроком ожидания. Если этот
срок истёк, сохранённый ID позволяет продолжить проверку позже.

```go
// waitForTranscript проверяет статус запуска и возвращает его готовый текст расшифровки.
func waitForTranscript(
	ctx context.Context, client *mindvoice.Client, workspaceID, runID string,
) (*mindvoice.TranscriptItemResponse, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		current, err := client.GetTranscriptionRun(ctx, workspaceID, runID)
		if err != nil {
			return nil, err
		}

		switch status := current.Run.Execution.Status; status {
		case mindvoice.TranscriptionRunItemExecutionStatusCompleted:
			return client.GetTranscript(ctx, workspaceID, runID)
		case mindvoice.TranscriptionRunItemExecutionStatusFailed,
			mindvoice.TranscriptionRunItemExecutionStatusCancelled:
			return nil, fmt.Errorf("транскрибация %s завершилась со статусом %s", runID, status)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
```

В результате `Transcript.Content.Segments` содержит сегменты текста, а
`Transcript.ID` идентифицирует конкретную версию расшифровки. Для ручного
анализа вызовите `client.ListAnalysisScenarios(ctx, workspaceID, nil)` и выберите
ID сценария (`sca-…`) из `Scenarios`. Передайте его и `Transcript.ID` в `StartAnalysisRun`.

```go
// startAnalysis запускает анализ выбранной версии текста расшифровки.
func startAnalysis(
	ctx context.Context, client *mindvoice.Client, workspaceID, recordingID, scenarioID, transcriptID string,
) (string, error) {
	started, err := client.StartAnalysisRun(ctx, workspaceID, recordingID, mindvoice.AnalysisRunCreate{
		AnalysisScenarioID: scenarioID,
		TranscriptID:       &transcriptID,
	})
	if err != nil {
		return "", err
	}
	return started.Run.ID, nil
}
```

Сохраните ID запуска анализа (`anl-…`). В `GetAnalysisRun` проверяйте
`Run.Execution.Status` до `completed`, `failed` или `cancelled`. После
`completed` вызовите
`client.GetRecordingAnalysisResult(ctx, workspaceID, recordingID, analysisRunID, nil)`.
Ответы на критерии находятся в `Result.Result.Answers`, резюме — в
необязательном поле `Result.Result.Summary`.
[Получение результата анализа](https://developer.mindvoice.pro/docs/guides/upload-and-results/#4-получите-ai-анализ)

## Скачивание файлов

`ExportTranscript` поддерживает `mindvoice.FormatSRT`, `mindvoice.FormatVTT`,
`mindvoice.FormatTXT` и `mindvoice.FormatTXTTimestamps`. `DownloadRecording`
возвращает исходное аудио. Для полного файла передайте `nil` в параметрах
скачивания. Для диапазона передайте `*mindvoice.DownloadRecordingParams` с
заголовком `Range`, например `bytes=0-1048575`. При частичном ответе
`StatusCode` равен `206`, а в `Header` доступен `Content-Range`.

Оба метода возвращают `*mindvoice.FileResponse` с открытым `Body`. Читайте
его до отмены контекста и закройте после чтения. Например, экспорт можно
записать в заранее открытый файл или другой `io.Writer`.

```go
// exportSRT записывает субтитры выбранного запуска в переданный поток.
func exportSRT(ctx context.Context, client *mindvoice.Client, workspaceID, runID string, dst io.Writer) error {
	result, err := client.ExportTranscript(ctx, workspaceID, runID, mindvoice.FormatSRT)
	if err != nil {
		return err
	}
	defer result.Body.Close()

	_, err = io.Copy(dst, result.Body)
	return err
}
```

Для этого фрагмента добавьте импорт `io`. Выходной файл закрывает код, который
его открыл.
[Форматы и параметры экспорта](https://developer.mindvoice.pro/docs/guides/upload-and-results/#скачивание-расшифровки)

## Списки и пагинация

Параметры списков передаются отдельной структурой. При обходе страниц
сохраняйте фильтры и сортировку, а `Offset` увеличивайте на число полученных
элементов. Завершайте обход на пустой странице или при достижении
`Pagination.TotalCount`, если сервер вернул это поле.

```go
// printRecordings выводит идентификаторы записей, обходя страницы списка.
func printRecordings(ctx context.Context, client *mindvoice.Client, workspaceID string) error {
	pageSize, offset := int32(50), int32(0)
	for {
		page, err := client.ListRecordings(ctx, workspaceID, &mindvoice.ListRecordingsParams{
			PageSize: &pageSize,
			Offset:   &offset,
		})
		if err != nil {
			return err
		}

		for _, recording := range page.Recordings {
			fmt.Println(recording.ID)
		}
		if len(page.Recordings) == 0 {
			return nil
		}
		offset += int32(len(page.Recordings))
		if total := page.Pagination.TotalCount; total != nil && int(offset) >= *total {
			return nil
		}
	}
}
```

[Правила пагинации и параметры списков](https://developer.mindvoice.pro/docs/guides/pagination/)

## Ошибки и повторы

Используйте `errors.As` для получения `*mindvoice.HTTPError`. Для логики
интеграции опирайтесь на `StatusCode` и `Code`. Поле `Violations` содержит
ошибки отдельных полей, а `RequestID` пригодится при обращении в поддержку.
Сетевые ошибки, отмену и истечение срока контекста можно проверять отдельно
через стандартные средства Go.

```go
// reportError выводит статус и идентификатор запроса для диагностики.
func reportError(err error) {
	if err == nil {
		return
	}

	var apiErr *mindvoice.HTTPError
	switch {
	case errors.As(err, &apiErr):
		log.Printf("MindVoice: HTTP %d, код %s", apiErr.StatusCode, apiErr.Code)
		if apiErr.RequestID != nil {
			log.Printf("MindVoice: ID запроса %s", *apiErr.RequestID)
		}
	case errors.Is(err, context.DeadlineExceeded):
		log.Print("MindVoice: истёк срок ожидания")
	case errors.Is(err, context.Canceled):
		log.Print("MindVoice: запрос отменён")
	default:
		log.Printf("MindVoice: ошибка запроса (%T)", err)
	}
}
```

Для этого фрагмента добавьте импорт `errors`.

| Статус | Что проверить |
| --- | --- |
| `401` | Секрет ключа и срок его действия |
| `403` | Права ключа и Workspace, к которому он привязан |
| `404` | ID ресурса и готовность результата |
| `409` | Текущее состояние записи или запуска |
| `413` | Размер загружаемого файла |
| `422` | Формат запроса и поля из `Violations` |
| `429` | Частоту запросов и подсказку `RetryAfter`, если она есть |

Повторами управляет приложение. После сетевого сбоя при загрузке или запуске
сначала проверьте состояние операции, затем решите, нужен ли повтор. При
сбое фоновой задачи изучите ошибку в карточке запуска. Методы
`RetryTranscriptionRun` и `RetryAnalysisRun` при успешном повторе возвращают
новый ID запуска.
[Обработка ошибок](https://developer.mindvoice.pro/docs/guides/error-handling/) ·
[Диагностика](https://developer.mindvoice.pro/docs/help/troubleshooting/)

## Для сопровождающих SDK

`go generate ./...` читает опубликованный OpenAPI с портала и генерирует
`client.gen.go`. Правила в [mindvoice.overlay.yaml](openapi/mindvoice.overlay.yaml)
задают имена методов и представления отдельных типов Go. Если добавилась
операция, проверьте её публичное имя. Если изменился или исчез элемент,
на который ссылается правило, обновите overlay. После генерации проверьте
diff и выполните `go test ./...` и `go vet ./...`.
[Опубликованная спецификация OpenAPI](https://developer.mindvoice.pro/openapi.yaml)
