package yougile

import (
	"errors"
	"fmt"
	"slices"

	"github.com/kao73/virtual-office/internal/tracker"
)

// LinkDependsOn записывает, что key зависит от dependsOnKey: id уходит
// в virtual_office.depends_on, откуда его читают toTask и гейт очерёдности
// (pipeline.UnmetDependencies). Своей связи «блокирует» у YouGile нет, а
// apiData в интерфейсе не видно, поэтому человеку связь показывает заметка
// в чате задачи (design doc §3).
//
// Идемпотентно, как у mock: уже записанная пара — nil без единой записи.
//
// Заметка уходит до записи id. pipeline.linkChildren пропускает id, уже
// видные в Get(key).DependsOn: сбой после записи и до заметки потерял бы
// заметку навсегда, а при нашем порядке худший исход — её видимый дубль.
func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
	switch {
	case dependsOnKey == "":
		return errors.New("yougile: зависимость без ключа задачи")
	case dependsOnKey == key:
		return fmt.Errorf("yougile: задача %s не может зависеть от самой себя", key)
	}
	_, data, err := t.owned(key, by)
	if err != nil {
		return err
	}
	if slices.Contains(data.DependsOn, dependsOnKey) {
		return nil
	}
	// getRaw, а не load: зависимость может стоять вне колонок графа, и это
	// не ошибка. Пропавшая или удалённая — ErrNotFound.
	dep, err := t.getRaw(dependsOnKey)
	if err != nil {
		return fmt.Errorf("зависимость %s задачи %s: %w", dependsOnKey, key, err)
	}
	note := dependencyNote(dep)
	if err := t.postChat(key, note, messageHTML(note)); err != nil {
		return err
	}
	data.DependsOn = append(data.DependsOn, dependsOnKey)
	return t.putTask(key, map[string]any{"apiData": data.encode()})
}

// dependencyNote — заметка о зависимости для человека: номер задачи в
// проекте, а без него — её id.
func dependencyNote(dep taskDTO) string {
	ref := dep.IDTaskProject
	if ref == "" {
		ref = dep.ID
	}
	return fmt.Sprintf("Зависит от: %s «%s»", ref, dep.Title)
}
