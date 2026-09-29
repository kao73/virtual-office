package yougile

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/kao73/virtual-office/internal/tracker"
)

// Вложения живут там, куда их кладёт интерфейс YouGile: сообщением-файлом
// в чате задачи и ссылкой в описании. Своего манифеста в apiData нет
// (design doc §4, §7): один механизм и для файлов человека, и для файлов
// офиса. id вложения — uuid из пути /user-data/<uuid>/<имя>.

// fileMessagePrefix — текст сообщения-файла в чате: /root/#file:<путь>.
const fileMessagePrefix = "/root/#file:"

var (
	// userDataPath — путь файла YouGile; сегмент имени берётся как есть,
	// закодированным.
	userDataPath = regexp.MustCompile(`^/user-data/([0-9a-fA-F-]{36})/([^/?#\s\\]+)$`)
	// anchorPattern — ссылка в HTML описания: href в двойных или одинарных
	// кавычках (группы 1 и 2) и текст (группа 3).
	anchorPattern = regexp.MustCompile(`(?is)<a\b[^>]*?\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>(.*?)</a>`)
	// tagPattern — теги внутри текста ссылки.
	tagPattern = regexp.MustCompile(`(?s)<[^>]*>`)
)

// fileLink — найденная ссылка на файл задачи.
type fileLink struct {
	ID      string // uuid файла — он же id вложения
	Segment string // последний сегмент пути как найден, закодированный: из него пересобирается URL
	Name    string // имя для человека и для файла в рабочей папке агента
}

// fileLinks — ссылки на файлы задачи: сначала из описания, потом из чата
// (msgs — от старых к новым, удалённые сообщения вызывающий уже отбросил).
// Один uuid — одна ссылка, выигрывает первая.
func fileLinks(description string, msgs []messageDTO) []fileLink {
	var links []fileLink
	seen := map[string]bool{}
	add := func(l fileLink, ok bool) {
		if !ok || seen[l.ID] {
			return
		}
		seen[l.ID] = true
		links = append(links, l)
	}
	for _, m := range anchorPattern.FindAllStringSubmatch(description, -1) {
		href := m[1]
		if href == "" {
			href = m[2]
		}
		add(descriptionLink(href, m[3]))
	}
	for _, m := range msgs {
		add(chatFileLink(m.Text))
	}
	return links
}

// chatFileLink — сообщение-файл: весь текст (без пробелов по краям) —
// /root/#file:/user-data/<uuid>/<имя>.
func chatFileLink(text string) (fileLink, bool) {
	path, ok := strings.CutPrefix(strings.TrimSpace(text), fileMessagePrefix)
	if !ok {
		return fileLink{}, false
	}
	return userDataLink(path)
}

// descriptionLink — ссылка из описания на любом хосте; хост и запрос
// отбрасываются (скачивание всё равно идёт с BaseURL). Имя — текст ссылки,
// а пустой текст — имя из пути.
func descriptionLink(href, text string) (fileLink, bool) {
	u, err := url.Parse(html.UnescapeString(strings.TrimSpace(href)))
	if err != nil {
		return fileLink{}, false
	}
	l, ok := userDataLink(u.EscapedPath())
	if !ok {
		return fileLink{}, false
	}
	if name := strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(text, ""))); name != "" {
		l.Name = name
	}
	return l, true
}

// userDataLink разбирает путь /user-data/<uuid>/<сегмент>. Сегмент, который
// раскодируется в пусто, «.» или «..», сдвинул бы пересобранный URL — это
// не вложение.
func userDataLink(escapedPath string) (fileLink, bool) {
	m := userDataPath.FindStringSubmatch(escapedPath)
	if m == nil || !tracker.ValidAttachmentID(m[1]) {
		return fileLink{}, false
	}
	if !escapedSegmentSafe(m[2]) {
		return fileLink{}, false
	}
	name := decodeName(m[2])
	if name == "" || name == "." || name == ".." {
		return fileLink{}, false
	}
	return fileLink{ID: m[1], Segment: m[2], Name: name}, true
}

// escapedSegmentSafe: сегмент — корректно экранированный, и на каждом из
// двух уровней раскодирования в нём нет «/» и «\». Иначе пересобранный URL
// (BaseURL/user-data/<uuid>/<сегмент>) вышел бы из папки файла: YouGile
// раскодирует путь один-два раза (design doc §4.3). Битый escape на первом
// уровне — тоже отказ: такую ссылку не скачать.
func escapedSegmentSafe(segment string) bool {
	name := segment
	for i := range 2 {
		next, err := url.PathUnescape(name)
		if err != nil {
			return i > 0
		}
		if strings.ContainsAny(next, `/\`) {
			return false
		}
		if next == name {
			break
		}
		name = next
	}
	return true
}

// decodeName раскодирует имя не больше двух раз: в чате YouGile кодирует
// его дважды, в описании — один раз. Остановка — на первой неудаче или
// когда декодирование ничего не меняет: буквальный «%» в имени иначе
// превратился бы в мусор.
func decodeName(segment string) string {
	name := segment
	for range 2 {
		next, err := url.PathUnescape(name)
		if err != nil || next == name {
			break
		}
		name = next
	}
	return name
}

// attachmentRefs — ссылки в модели раннера; nil, если вложений нет.
func attachmentRefs(links []fileLink) []tracker.AttachmentRef {
	var refs []tracker.AttachmentRef
	for _, l := range links {
		refs = append(refs, tracker.AttachmentRef{ID: l.ID, Name: l.Name})
	}
	return refs
}

// AddAttachment загружает файл и прикрепляет его к задаче сообщением-файлом
// в чате — так же, как это делает интерфейс: человек видит файл, Get находит
// его среди вложений. Отдаёт uuid файла.
//
// Сбой между загрузкой и сообщением оставляет невидимую осиротевшую
// загрузку и возвращает ошибку; повтор загрузит заново (design doc §4.2).
func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
	if _, _, err := t.owned(key, by); err != nil {
		return "", err
	}
	uploaded, err := t.upload(name, data)
	if err != nil {
		return "", err
	}
	link, ok := uploadedLink(uploaded)
	if !ok {
		return "", fmt.Errorf("yougile: upload-file вернул url %q не вида /user-data/<uuid>/<имя> — файл загружен, но к задаче %s не привязан",
			uploaded, key)
	}
	// Текст — url ровно так, как его отдал сервер (design doc §4.2), и он же
	// в textHtml без HTML-экранирования.
	text := fileMessagePrefix + uploaded
	if err := t.postChat(key, text, text); err != nil {
		return "", err
	}
	return link.ID, nil
}

// uploadedLink — ссылка из ответа upload-file: путь без хоста и запроса,
// как в сообщении-файле.
func uploadedLink(raw string) (fileLink, bool) {
	return userDataLink(raw)
}

// GetAttachment читает вложение по uuid. Ссылку ищет там же, где Get (описание
// и чат), URL пересобирает на BaseURL и скачивает клиентом без ключа API
// (design doc §4.3). Три запроса — задача, чат, файл; кэша нет намеренно.
func (t *Tracker) GetAttachment(key, id string) ([]byte, error) {
	if !tracker.ValidAttachmentID(id) {
		return nil, fmt.Errorf("%w: вложение %s/%s", tracker.ErrNotFound, key, id)
	}
	raw, err := t.getRaw(key)
	if err != nil {
		return nil, err
	}
	msgs, err := t.chat(key)
	if err != nil {
		return nil, err
	}
	for _, link := range fileLinks(raw.Description, msgs) {
		if link.ID == id {
			return t.download(link)
		}
	}
	return nil, fmt.Errorf("%w: вложение %s/%s не упомянуто ни в описании, ни в чате задачи",
		tracker.ErrNotFound, key, id)
}

// download — GET файла по пути, пересобранному на BaseURL: хост из ссылки
// в описании не используется никогда. Хост API отвечает 302 в хранилище,
// клиент files идёт туда без Authorization.
func (t *Tracker) download(link fileLink) ([]byte, error) {
	target := t.cfg.BaseURL + "/user-data/" + link.ID + "/" + link.Segment
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("вложение %s: запрос не собран: %w", link.ID, err)
	}
	resp, err := t.files.Do(req)
	if err != nil {
		return nil, fmt.Errorf("вложение %s не скачано: %w", link.ID, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: вложение %s (404)", tracker.ErrNotFound, link.ID)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("вложение %s: %d: %s", link.ID, resp.StatusCode, snippet(body))
	case readErr != nil:
		return nil, fmt.Errorf("вложение %s: ответ не дочитан: %w", link.ID, readErr)
	}
	return body, nil
}
