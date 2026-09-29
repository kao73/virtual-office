package yougile

import (
	"reflect"
	"testing"

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

func TestAttachmentRefs(t *testing.T) {
	if got := attachmentRefs(nil); got != nil {
		t.Errorf("пусто дало %+v", got)
	}
	got := attachmentRefs([]fileLink{{ID: uuid1, Segment: "a%20b.txt", Name: "a b.txt"}})
	if want := []tracker.AttachmentRef{{ID: uuid1, Name: "a b.txt"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("attachmentRefs = %+v", got)
	}
}
