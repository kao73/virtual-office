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
	name, ok := decodeSegment(m[2])
	if !ok || name == "" || name == "." || name == ".." {
		return fileLink{}, false
	}
	return fileLink{ID: m[1], Segment: m[2], Name: name}, true
}

// decodeSegment раскодирует сегмент имени не больше двух раз — в чате
// YouGile кодирует имя дважды, в описании один раз — и заодно проверяет, что
// он безопасен для пересборки URL (BaseURL/user-data/<uuid>/<сегмент>):
//   - битый escape на первом уровне — отказ, такую ссылку не скачать;
//   - неудача на втором — остаёмся на первом: буквальный «%» в имени;
//   - «/» или «\» на любом уровне — отказ: YouGile раскодирует путь
//     один-два раза, и URL вышел бы из папки файла (design doc §4.3);
//   - декодирование, которое ничего не меняет, — остановка.
func decodeSegment(segment string) (string, bool) {
	name := segment
	for i := range 2 {
		next, err := url.PathUnescape(name)
		if err != nil {
			if i == 0 {
				return "", false
			}
			return name, true
		}
		if strings.ContainsAny(next, `/\`) {
			return "", false
		}
		if next == name {
			break
		}
		name = next
	}
	return name, true
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
	link, ok := userDataLink(uploaded)
	if !ok {
		return "", fmt.Errorf("yougile: upload-file вернул url %q не вида /user-data/<uuid>/<имя> — файл загружен, но к задаче %s не привязан",
			uploaded, key)
	}
	// Текст — url ровно так, как его отдал сервер (design doc §4.2), а textHtml —
	// он же, экранированный как HTML: сегмент имени пропускает «&», «<», «>»
	// и «"». Корректный url экранирование не меняет.
	text := fileMessagePrefix + uploaded
	if err := t.postChat(key, text, html.EscapeString(text)); err != nil {
		return "", err
	}
	return link.ID, nil
}

// GetAttachment читает вложение по uuid. Ссылку ищет там же, где Get (описание
// и чат), URL пересобирает на BaseURL и скачивает клиентом без ключа API
// (design doc §4.3). Запросов два — задача и файл, — если ссылка нашлась
// в описании, и три — с чатом, — если нет; кэша нет намеренно.
func (t *Tracker) GetAttachment(key, id string) ([]byte, error) {
	if !tracker.ValidAttachmentID(id) {
		return nil, fmt.Errorf("%w: вложение %s/%s", tracker.ErrNotFound, key, id)
	}
	raw, err := t.getRaw(key)
	if err != nil {
		return nil, err
	}
	// Описание просматривается первым и у fileLinks выигрывает: найдись id
	// там, чат ответа уже не изменит — запрос к нему не нужен.
	if link, ok := findLink(fileLinks(raw.Description, nil), id); ok {
		return t.download(link)
	}
	msgs, err := t.chat(key)
	if err != nil {
		return nil, err
	}
	if link, ok := findLink(fileLinks(raw.Description, msgs), id); ok {
		return t.download(link)
	}
	return nil, fmt.Errorf("%w: вложение %s/%s не упомянуто ни в описании, ни в чате задачи",
		tracker.ErrNotFound, key, id)
}

// findLink — ссылка с данным uuid.
func findLink(links []fileLink, id string) (fileLink, bool) {
	for _, l := range links {
		if l.ID == id {
			return l, true
		}
	}
	return fileLink{}, false
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
