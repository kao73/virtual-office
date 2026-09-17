package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
)

// namedOffice — офис и имя его трекера: у pipeline.Office имени нет,
// а обход и заголовки в выводе — забота этой обёртки, не пайплайна.
type namedOffice struct {
	name string
	*pipeline.Office
}

// offices — все офисы этого раннера, по одному на трекер, в порядке
// TrackersInUse. Пайплайн о множественности не знает: он получает один
// офис и работает в нём, как и раньше.
//
// Хозяйство рабочих папок общее на машину, а не на офис, и лежит здесь
// отдельно: worktree rm перечисляет папки раньше, чем знает, чей проект.
type offices struct {
	list       []namedOffice
	workspaces *workspace.Manager
	out        io.Writer
}

// each обходит офисы по порядку для разовой команды. Заголовок
// «== трекер jira ==» печатается только когда офисов больше одного: при
// одном stdout совпадает с прежним байт в байт, а ошибка и тогда несёт имя
// трекера в префиксе. Ошибка одного офиса не останавливает остальных — все
// собираются errors.Join и уходят наверх разом.
func (all *offices) each(ctx context.Context, fn func(namedOffice) error) error {
	return all.visit(ctx, func(no namedOffice) error {
		if len(all.list) > 1 {
			fmt.Fprintf(all.out, "== трекер %s ==\n", no.name)
		}
		return fn(no)
	})
}

// visit — сам обход, без заголовков: их печатает each для разовой команды,
// а заход loop (cycle) обходится без них — при --every 2m заголовки дали бы
// тысячу строк в сутки в журнале планировщика.
//
// ctx проверяется перед каждым офисом, а не только между заходами: сигнал,
// пришедший в офисе jira, не даёт начаться mock. Сам прогон он при этом
// прерывает (см. loopCommand). Накопленное к этому моменту возвращается.
func (all *offices) visit(ctx context.Context, fn func(namedOffice) error) error {
	var errs []error
	for _, no := range all.list {
		if ctx.Err() != nil {
			break
		}
		if err := fn(no); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", no.name, err))
		}
	}
	return errors.Join(errs...)
}

// byProject — офис, которому принадлежит проект: для worktree rm и
// ls --project. У задачи есть проект, у проекта — трекер, у трекера — офис.
func (all *offices) byProject(key string) (namedOffice, error) {
	for _, no := range all.list {
		if _, mine := no.Projects[key]; mine {
			return no, nil
		}
	}
	return namedOffice{}, fmt.Errorf("проект %q не описан в %s", key, tracker.ProjectsLocalFile)
}

// loop гоняет цикл по расписанию, пока не остановят.
//
// Это не демон и не supervisor: он не следит за собой, не перезапускается
// и не держит состояния между заходами. Драйвер переехал сюда из
// pipeline.Office.Loop, когда офисов стало несколько; сам заход — cycle.
func (all *offices) loop(ctx context.Context, every time.Duration, role string) error {
	for {
		all.cycle(ctx, role)

		select {
		case <-ctx.Done():
			fmt.Fprintln(all.out, "остановка по сигналу")
			return nil
		case <-time.After(every):
		}
	}
}

// cycle — один заход цикла: по каждому офису reap → tick → complete-splits.
// Ошибка шага — повод сказать о ней и пойти дальше, а не умереть: следующий
// заход может пройти. Поэтому fn всегда возвращает nil, а visit здесь нужен
// ради порядка обхода и остановки между офисами. Чей шаг упал, говорит
// префикс строки: заголовков заход не печатает.
//
// CompleteSplits идёт после tick, а не до него, и порядок не косметика:
// tick первым делом разбирает ответы человека (HumanReplies), и реплика,
// пришедшая между заходами, обязана увести задачу из Blocked раньше, чем
// до неё дойдёт CompleteSplits. Иначе тикет с двумя подтверждающими
// split-маркерами всё ещё лежал бы в Blocked, когда CompleteSplits его
// увидит, и автосоздание детей состоялось бы вопреки ответу, которого
// никто ещё не прочитал. Reap перед tick не переставлен: он разбирает
// задачи с истёкшей арендой, а не задачи в Blocked.
func (all *offices) cycle(ctx context.Context, role string) {
	_ = all.visit(ctx, func(no namedOffice) error {
		if err := no.Reap(ctx); err != nil {
			fmt.Fprintf(all.out, "%s: reap: %v\n", no.name, err)
		}
		if _, err := tick(ctx, no, role); err != nil {
			fmt.Fprintf(all.out, "%s: tick: %v\n", no.name, err)
		}
		if err := no.CompleteSplits(ctx); err != nil {
			fmt.Fprintf(all.out, "%s: complete-splits: %v\n", no.name, err)
		}
		return nil
	})
}

// tick — цикл одного офиса: по всем ролям графа или по одной названной.
// Один на команду tick и на заход loop. Нашлась ли работа, известно только
// для названной роли — TickAll этого не считает, и для него ответ всегда
// false, на который вызывающий не смотрит. Что делать с ответом, решает
// вызывающий: команда говорит «работы нет» человеку, loop молчит — иначе
// лог планировщика рос бы этой строкой каждые две минуты.
func tick(ctx context.Context, no namedOffice, role string) (bool, error) {
	if role == "" {
		return false, no.TickAll(ctx)
	}
	return no.Tick(ctx, role)
}
