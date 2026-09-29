package yougile

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

const (
	uuid1 = "11111111-2222-4333-8444-555555555555"
	uuid2 = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// Файл, прикреплённый человеком в чат: имя закодировано дважды (design doc §1).
func TestChatFileLinkDecodesNameTwice(t *testing.T) {
	l, ok := chatFileLink("  /root/#file:/user-data/" + uuid1 + "/%25D0%25A2%25D0%2597.txt\n")
	want := fileLink{ID: uuid1, Segment: "%25D0%25A2%25D0%2597.txt", Name: "ТЗ.txt"}
	if !ok || l != want {
		t.Errorf("chatFileLink = %+v, %v; ожидалось %+v", l, ok, want)
	}
}

func TestChatFileLinkRejectsNonFileText(t *testing.T) {
	for _, text := range []string{
		"вот файл /root/#file:/user-data/" + uuid1 + "/a.txt", // не с начала
		"/root/#file:/user-data/" + uuid1 + "/a/b.txt",        // лишний сегмент
		"/root/#file:/user-data/not-a-uuid/a.txt",             // не uuid
		"/root/#file:/user-data/" + uuid1 + "/a.txt?x=1",      // хвост
		"/root/#file:https://evil.example/user-data/" + uuid1 + "/a.txt",
		"/root/#file:/user-data/" + uuid1 + "/a%zz.txt",      // битый escape
		"/root/#file:/user-data/" + uuid1 + "/a.txt\nсмотри", // перевод строки внутри
		"/root/#file:/user-data/" + uuid1 + "/a b.txt",       // пробел внутри
		"/root/#file:/user-data/" + uuid1 + "/a\tb.txt",      // управляющий символ
		"/root/#file:/user-data/" + uuid1 + "/a\\b",          // сырой обратный слэш
	} {
		if l, ok := chatFileLink(text); ok {
			t.Errorf("%q принят как файл: %+v", text, l)
		}
	}
}

// Review Focus #2: буквальный «%» в имени — второе декодирование падает,
// останавливаемся на первом.
func TestDecodeNameStopsOnFailure(t *testing.T) {
	for segment, want := range map[string]string{
		"100%25.txt":            "100%.txt",
		"plain.txt":             "plain.txt",
		"%25D0%25A2.txt":        "Т.txt",
		"%252525.txt":           "%25.txt", // не больше двух раз
		"%D0%A2%D0%97%20v2.pdf": "ТЗ v2.pdf",
	} {
		if got := decodeName(segment); got != want {
			t.Errorf("decodeName(%q) = %q, ожидалось %q", segment, got, want)
		}
	}
}

// Review Focus #1: ссылка в описании — на ru.yougile.com, с previews[] и
// &amp; в запросе, текст ссылки бывает обёрнут в теги.
func TestFileLinksFromDescriptionHTML(t *testing.T) {
	description := `<p>Постановка:</p><p><a href="https://ru.yougile.com/user-data/` + uuid1 +
		`/%D0%A2%D0%97.pdf?previews[]=a&amp;previews[]=b" target="_blank"><span>ТЗ</span>.pdf</a></p>` +
		`<p><a href="https://ru.yougile.com/user-data/` + uuid2 + `/scheme.png"></a></p>` +
		`<p><a href="https://example.com/doc">не файл</a></p>`
	got := fileLinks(description, nil)
	want := []fileLink{
		{ID: uuid1, Segment: "%D0%A2%D0%97.pdf", Name: "ТЗ.pdf"},
		{ID: uuid2, Segment: "scheme.png", Name: "scheme.png"}, // пустой текст — имя из пути
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileLinks =\n%+v\nожидалось\n%+v", got, want)
	}
}

// Атрибут href может быть в одинарных кавычках — принимаем и их.
func TestFileLinksSingleQuotedHref(t *testing.T) {
	description := `<a target='_blank' href='https://ru.yougile.com/user-data/` + uuid1 + `/a.txt?x=1'>A.txt</a>`
	got := fileLinks(description, nil)
	want := []fileLink{{ID: uuid1, Segment: "a.txt", Name: "A.txt"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileLinks =\n%+v\nожидалось\n%+v", got, want)
	}
}

// HTML-сущность в href раскрывается до разбора: «&amp;» в имени — это «&».
// Сущность только в запросе на путь не влияет, поэтому проверяем именно путь.
func TestDescriptionLinkUnescapesEntitiesInHref(t *testing.T) {
	href := "https://ru.yougile.com/user-data/" + uuid1 + "/a&amp;b.txt?a=1&amp;previews[]=x"
	l, ok := descriptionLink(href, "")
	want := fileLink{ID: uuid1, Segment: "a&b.txt", Name: "a&b.txt"}
	if !ok || l != want {
		t.Errorf("descriptionLink = %+v, %v; ожидалось %+v", l, ok, want)
	}
}

func TestFileLinksPlainTextDescriptionHasNone(t *testing.T) {
	if got := fileLinks("просто текст /user-data/"+uuid1+"/a.txt без ссылки", nil); got != nil {
		t.Errorf("из простого текста: %+v", got)
	}
}

// Порядок: описание, потом чат от старых к новым; один uuid — одна ссылка,
// выигрывает первая.
func TestFileLinksOrderAndDedup(t *testing.T) {
	description := `<a href="https://ru.yougile.com/user-data/` + uuid1 + `/spec.pdf">Спека.pdf</a>`
	msgs := []messageDTO{
		{ID: 1, Text: "обычный текст"},
		{ID: 2, Text: "/root/#file:/user-data/" + uuid2 + "/b.txt"},
		{ID: 3, Text: "/root/#file:/user-data/" + uuid1 + "/spec.pdf"},
	}
	got := fileLinks(description, msgs)
	want := []fileLink{
		{ID: uuid1, Segment: "spec.pdf", Name: "Спека.pdf"},
		{ID: uuid2, Segment: "b.txt", Name: "b.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileLinks =\n%+v\nожидалось\n%+v", got, want)
	}
}

// Review Focus #5: сегмент, который сдвинул бы пересобранный URL, — не вложение.
func TestUserDataLinkRejectsDotSegments(t *testing.T) {
	for _, segment := range []string{".", "..", "%2E%2E", "%252E%252E"} {
		if l, ok := userDataLink("/user-data/" + uuid1 + "/" + segment); ok {
			t.Errorf("сегмент %q принят: %+v", segment, l)
		}
	}
}

// Design §4.3: пересобранный URL остаётся под /user-data/<uuid>/. Разделитель
// пути на любом уровне раскодирования (в самом имени или закодированный один
// или два раза) выводит за эту папку — не вложение.
func TestUserDataLinkRejectsEncodedSeparators(t *testing.T) {
	for _, segment := range []string{
		"..%2F..%2Fx", "%2E%2E%2F", "a%2F%2E%2E", "a%252F..", "..%5Cx", "a%255Cb", "a%2Fb",
	} {
		if l, ok := userDataLink("/user-data/" + uuid1 + "/" + segment); ok {
			t.Errorf("сегмент %q принят: %+v", segment, l)
		}
	}
}

// Буквальный «%» в имени (второе раскодирование падает) — нормальное вложение,
// а не битый escape. Тест фиксирует уже существующее поведение.
func TestUserDataLinkAcceptsLiteralPercent(t *testing.T) {
	l, ok := userDataLink("/user-data/" + uuid1 + "/100%25.txt")
	want := fileLink{ID: uuid1, Segment: "100%25.txt", Name: "100%.txt"}
	if !ok || l != want {
		t.Errorf("userDataLink = %+v, %v; ожидалось %+v", l, ok, want)
	}
}

func TestAttachmentRefs(t *testing.T) {
	if got := attachmentRefs(nil); got != nil {
		t.Errorf("пусто дало %+v", got)
	}
	got := attachmentRefs([]fileLink{{ID: uuid1, Segment: "a%20b.txt", Name: "a b.txt"}})
	if want := []tracker.AttachmentRef{{ID: uuid1, Name: "a b.txt"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("attachmentRefs = %+v", got)
	}
}

func TestAddAttachmentUploadsAndPostsFileMessage(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	data := []byte("{\"children\":[]}\n")
	id, err := tr.AddAttachment(testKey, tracker.ByRun("run-1"), "split.json", data)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := fake.uploads[id]
	if !ok || file.Name != "split.json" || string(file.Data) != string(data) {
		t.Errorf("загружено: %q → %+v", id, fake.uploads)
	}
	wantText := "/root/#file:/user-data/" + id + "/split.json"
	if len(fake.chatPosts) != 1 || fake.chatPosts[0]["text"] != wantText || fake.chatPosts[0]["textHtml"] != wantText {
		t.Errorf("сообщение-файл: %#v, ожидался text = textHtml = %q", fake.chatPosts, wantText)
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if want := []tracker.AttachmentRef{{ID: id, Name: "split.json"}}; !reflect.DeepEqual(task.Attachments, want) {
		t.Errorf("Attachments = %+v", task.Attachments)
	}
	if last := task.Comments[len(task.Comments)-1]; last.Body != "[вложение: split.json]" || last.Author != "office@example.com" {
		t.Errorf("реплика офиса: %+v", last)
	}
}

// text и textHtml сообщения-файла — одна и та же строка, без HTML-экранирования:
// иначе «&» в имени стал бы «&amp;» и интерфейс не узнал бы файл.
func TestAddAttachmentTextEqualsHTMLForAmpersand(t *testing.T) {
	tr, fake := fixture(t)
	if _, err := tr.AddAttachment(testKey, tracker.BySystem(), "a&b.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(fake.chatPosts) != 1 || fake.chatPosts[0]["text"] != fake.chatPosts[0]["textHtml"] {
		t.Errorf("text и textHtml разошлись: %#v", fake.chatPosts)
	}
}

func TestAddAttachmentKeepsNonASCIIName(t *testing.T) {
	tr, fake := fixture(t)
	id, err := tr.AddAttachment(testKey, tracker.BySystem(), "ТЗ v2.pdf", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := tr.Get(testKey)
	if len(task.Attachments) != 1 || task.Attachments[0] != (tracker.AttachmentRef{ID: id, Name: "ТЗ v2.pdf"}) {
		t.Errorf("Attachments = %+v", task.Attachments)
	}
	if fake.uploads[id].Name != "ТЗ v2.pdf" {
		t.Errorf("имя в multipart: %q", fake.uploads[id].Name)
	}
}

// Файл загружен, но url не того вида — привязать нечего: ошибка, и в чат
// ничего не уходит (осиротевшая загрузка безвредна, design doc §4.2).
func TestAddAttachmentRejectsMalformedUploadAnswer(t *testing.T) {
	for name, u := range map[string]string{
		"не user-data": "/files/whatever.txt",
		"не uuid":      "/user-data/12345/a.txt",
		"пусто":        "",
	} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			fake.uploadURL = func(string, string) string { return u }
			if _, err := tr.AddAttachment(testKey, tracker.BySystem(), "a.txt", []byte("x")); err == nil {
				t.Error("кривой ответ upload-file принят")
			}
			if len(fake.chatPosts) != 0 {
				t.Errorf("в чат ушло: %#v", fake.chatPosts)
			}
		})
	}
}

func TestAddAttachmentUploadFailureNeverLeaksKey(t *testing.T) {
	tr, fake := fixture(t)
	fake.fail["POST /api-v2/upload-file"] = http.StatusInternalServerError
	_, err := tr.AddAttachment(testKey, tracker.BySystem(), "a.txt", []byte("x"))
	if err == nil || strings.Contains(err.Error(), "test-key") || !strings.Contains(err.Error(), "500") {
		t.Errorf("отказ загрузки дал %v", err)
	}
	if len(fake.chatPosts) != 0 {
		t.Error("сообщение ушло без файла")
	}
}

func TestFileHostAllowed(t *testing.T) {
	cases := []struct {
		base, target string
		want         bool
	}{
		{"https://yougile.com", "https://yougile.com/user-data/x", true},
		{"https://yougile.com", "https://prod-user-data.yougile.com/x", true},
		{"https://yougile.com", "https://PROD-USER-DATA.YouGile.com/x", true},
		{"https://YouGile.COM", "https://prod-user-data.yougile.com/x", true}, // регистр у BaseURL
		{"https://yougile.com", "https://a.b.yougile.com/x", true},            // поддомен глубже
		{"https://yougile.com", "HTTPS://prod-user-data.yougile.com/x", true}, // регистр схемы
		{"https://yougile.com", "https://yougile.com:8443/x", true},           // порт не сравнивается
		{"https://yougile.com:443", "https://prod-user-data.yougile.com/x", true},
		{"https://yougile.com", "http://prod-user-data.yougile.com/x", false}, // схема не та
		{"https://yougile.com", "http://yougile.com/x", false},                // схема не та, хост тот же
		{"http://yougile.com", "https://yougile.com/x", false},                // и в обратную сторону
		{"https://yougile.com", "https://evilyougile.com/x", false},           // не поддомен
		{"https://yougile.com", "https://yougile.com.evil.io/x", false},
		{"https://yougile.com", "https://yougile.com.evil.com/x", false},
		{"https://yougile.com", "https://evil.com/yougile.com/x", false},
		{"https://yougile.com", "https://yougile.com@evil.com/x", false}, // userinfo, хост evil.com
		{"https://yougile.com", "https://ru.yougile.co/x", false},
		{"https://yougile.com", "https://com/x", false}, // родитель, а не поддомен
		{"https://ru.yougile.com", "https://yougile.com/x", false},
		{"https://yougile.com", "/relative", false},    // без схемы и хоста
		{"https://:443", "https://evil.com./x", false}, // пустой хост BaseURL ничего не разрешает
		{"https://:443", "https://:443/x", false},
		{"http://127.0.0.1:1234", "http://127.0.0.1:9999/x", true}, // порт не важен — так ходят тесты
		{"http://127.0.0.1:1234", "http://localhost:1234/x", false},
	}
	for _, c := range cases {
		base, err := url.Parse(c.base)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", c.base, err)
		}
		target, err := url.Parse(c.target)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", c.target, err)
		}
		if got := fileHostAllowed(base, target); got != c.want {
			t.Errorf("fileHostAllowed(%s, %s) = %v", c.base, c.target, got)
		}
	}
}

// Цепочка перенаправлений ограничена: пять переходов — да, шестой — нет.
func TestFileClientCapsRedirects(t *testing.T) {
	var hits atomic.Int32
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	t.Cleanup(loop.Close)
	base, _ := url.Parse(loop.URL)
	_, err := newFileClient(base).Get(loop.URL + "/start")
	if err == nil || !strings.Contains(err.Error(), "перенаправлений") {
		t.Errorf("бесконечные перенаправления дали %v", err)
	}
	if got := hits.Load(); got != maxFileRedirects+1 {
		t.Errorf("запросов %d, ожидалось %d (первый и %d переходов)", got, maxFileRedirects+1, maxFileRedirects)
	}
}

// Ровно maxFileRedirects переходов проходят: пятый ещё разрешён.
func TestFileClientAllowsMaxRedirects(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= maxFileRedirects {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	resp, err := newFileClient(base).Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("%d перенаправлений отвергнуты: %v", maxFileRedirects, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("статус %d", resp.StatusCode)
	}
}

func TestFileClientRefusesForeignRedirect(t *testing.T) {
	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { foreignHits.Add(1) }))
	t.Cleanup(foreign.Close)
	foreignURL := strings.Replace(foreign.URL, "127.0.0.1", "localhost", 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreignURL+"/steal", http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	base, _ := url.Parse(origin.URL)
	if _, err := newFileClient(base).Get(origin.URL + "/user-data/x"); err == nil {
		t.Error("перенаправление на чужой хост пройдено")
	}
	if foreignHits.Load() != 0 {
		t.Error("чужой хост получил запрос")
	}
}

// Перенаправление со сменой схемы (http → https на том же хосте) отвергается
// до соединения.
func TestFileClientRefusesSchemeChangingRedirect(t *testing.T) {
	var tlsHits atomic.Int32
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { tlsHits.Add(1) }))
	t.Cleanup(secure.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL+"/x", http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	base, _ := url.Parse(origin.URL)
	client := newFileClient(base)
	client.Transport = secure.Client().Transport // TLS-сертификат тестового сервера принят
	_, err := client.Get(origin.URL + "/user-data/x")
	if err == nil || !strings.Contains(err.Error(), "не разрешено") {
		t.Errorf("смена схемы дала %v", err)
	}
	if tlsHits.Load() != 0 {
		t.Error("сервер с другой схемой получил запрос")
	}
}

// Ни один запрос клиента файлов не несёт Authorization: ни первый, ни
// перенаправленный — даже если вызывающий по ошибке поставил его сам, а
// перенаправление ведёт на тот же хост (net/http такой заголовок переносит).
func TestFileClientNeverSendsAuthorization(t *testing.T) {
	var seen atomic.Int32
	var leaked atomic.Value
	leaked.Store("")
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if a := r.Header.Get("Authorization"); a != "" {
			leaked.Store(a)
		}
		_, _ = w.Write([]byte("файл"))
	}))
	t.Cleanup(storage.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storage.URL+"/file", http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	base, _ := url.Parse(origin.URL)
	client := newFileClient(base)

	// Обычный путь: клиент сам заголовков не ставит.
	resp, err := client.Get(origin.URL + "/user-data/x")
	if err != nil {
		t.Fatalf("скачивание: %v", err)
	}
	resp.Body.Close()

	// Второй заслон: Authorization, поставленный вызывающим, на
	// перенаправлении снимается.
	req, _ := http.NewRequest(http.MethodGet, origin.URL+"/user-data/x", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("скачивание с заголовком: %v", err)
	}
	resp.Body.Close()

	if seen.Load() != 2 {
		t.Fatalf("хранилище получило %d запросов, ожидалось 2", seen.Load())
	}
	if a := leaked.Load().(string); a != "" {
		t.Errorf("хранилище получило Authorization %q", a)
	}
}

func TestOpenBuildsFileClient(t *testing.T) {
	tr, _ := fixture(t)
	if tr.files == nil || tr.files == tr.client || tr.files.CheckRedirect == nil {
		t.Errorf("клиент файлов: %+v", tr.files)
	}
}
