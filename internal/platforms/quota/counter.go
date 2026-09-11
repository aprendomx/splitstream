// Package quota cuenta las unidades de cuota de YouTube por cuenta y día. El día es el
// del Pacífico, que es donde Google reinicia la cuota. El contador es lo que el panel
// enseña y lo que decide cuándo pausar el chat: cuota visible, nunca silenciosa.
package quota

import (
	"context"
	"log/slog"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

type Counter struct {
	db     *store.DB
	Now    func() time.Time
	Logger *slog.Logger
	zona   *time.Location
}

func NewCounter(db *store.DB) *Counter {
	zona, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		// Sin tzdata (imagen scratch sin zoneinfo): UTC es peor que nada pero no rompe.
		zona = time.UTC
	}
	return &Counter{db: db, Now: time.Now, Logger: slog.Default(), zona: zona}
}

// Day es el día de cuota actual, YYYY-MM-DD en hora del Pacífico.
func (c *Counter) Day() string { return c.Now().In(c.zona).Format("2006-01-02") }

// Add suma unidades. Un fallo se loguea y no se propaga: contar la cuota nunca puede
// impedir la llamada que la gasta.
func (c *Counter) Add(ctx context.Context, accountID int64, units int) {
	if units <= 0 {
		return
	}
	if err := c.db.AddQuota(context.WithoutCancel(ctx), accountID, c.Day(), units); err != nil {
		c.Logger.Debug("no se pudo sumar la cuota", "cuenta", accountID, "err", err)
	}
}

func (c *Counter) UsedToday(ctx context.Context, accountID int64) (int, error) {
	return c.db.QuotaUsed(ctx, accountID, c.Day())
}

// Sink adapta Add a lo que el proveedor de YouTube recibe por cuenta.
func (c *Counter) Sink(accountID int64) platforms.QuotaSink {
	return func(units int) { c.Add(context.Background(), accountID, units) }
}
