package yougile

import (
	"html"
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
	userDataPath = regexp.MustCompile(`^/user-data/([0-9a-fA-F-]{36})/([^/?#]+)$`)
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
	name := decodeName(m[2])
	if name == "" || name == "." || name == ".." {
		return fileLink{}, false
	}
	return fileLink{ID: m[1], Segment: m[2], Name: name}, true
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
