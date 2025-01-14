package gormshadow

import (
	"context"
	"reflect"
	"strings"
	"time"

	"github.com/fatih/structs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const (
	gormCommit  = "gorm:commit_or_rollback_transaction"
	gormQuery   = "gorm:query"
	gormPreload = "gorm:preload"

	shadowCreate  = "shadow:create"
	shadowUpdate  = "shadow:update"
	shadowDelete  = "shadow:delete"
	shadowQuery   = "shadow:query"
	shadowPreload = "shadow:preload"
)

// TimeMachine is an interface that provides the desired timestamp for querying historical data.
type TimeMachine interface {
	GetTime(ctx context.Context) (time.Time, error)
}

// Plugin creates shadow entries for models that implement the Descriptor interface.
type Plugin struct {
	TimeMachine TimeMachine
}

// Name returns the name of the plugin.
func (p *Plugin) Name() string {
	return "shadow"
}

// Initialize registers the plugin with GORM.
func (p *Plugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()
	if cb.Create().Get(shadowCreate) == nil {
		if err := cb.Create().Before(gormCommit).Register(shadowCreate, p.BeforeCommit); err != nil {
			return err
		}
	}
	if cb.Update().Get(shadowUpdate) == nil {
		if err := cb.Update().Before(gormCommit).Register(shadowUpdate, p.BeforeCommit); err != nil {
			return err
		}
	}
	if cb.Delete().Get(shadowDelete) == nil {
		if err := cb.Delete().Before(gormCommit).Register(shadowDelete, p.BeforeDelete); err != nil {
			return err
		}
	}
	if cb.Query().Get(shadowQuery) == nil {
		if err := cb.Query().Before(gormQuery).Register(shadowQuery, p.BeforeQuery); err != nil {
			return err
		}
	}
	if cb.Query().Get(shadowPreload) == nil {
		if err := cb.Query().Before(gormPreload).Register(shadowPreload, p.BeforePreload); err != nil {
			return err
		}
	}
	return nil
}

// BeforeDelete creates a shadow entry for the model being deleted, if it is soft deleted.
func (p *Plugin) BeforeDelete(tx *gorm.DB) {
	stmt := tx.Statement
	if _, ok := stmt.Clauses["soft_delete_enabled"]; !ok {
		return
	}

	// At this point, the model is empty, so we need to get the model from the context
	// and pass the transaction to BeforeCommit
	queryDB := tx.Session(&gorm.Session{
		NewDB:       true,
		SkipHooks:   true,
		QueryFields: true,
	}).
		Unscoped().
		Model(stmt.Model)

	// Extract the unscoped WHERE clause from the query
	whereClause := stmt.Clauses["WHERE"]
	where := whereClause.Expression.(clause.Where)
	where.Exprs = where.Exprs[:len(where.Exprs)-1]
	whereClause.Expression = where
	queryDB.Statement.Clauses["WHERE"] = whereClause

	if err := queryDB.First(&stmt.Model).Error; err != nil {
		queryDB.Logger.Error(
			queryDB.Statement.Context,
			"Failed to get model for shadow entry: %v",
			err,
		)
		_ = queryDB.AddError(err)
		return
	}
	p.BeforeCommit(tx)
}

// BeforeCommit creates a shadow entry for the model being created.
func (p *Plugin) BeforeCommit(tx *gorm.DB) {
	if tx.Error != nil || tx.Statement.Schema == nil || tx.Statement.Model == nil {
		return
	}

	if desc, ok := tx.Statement.Model.(Descriptor); ok {
		// Get a new database session for creating the shadow entry.
		shadowDB := tx.Session(&gorm.Session{
			NewDB:     true,
			SkipHooks: true,
		})

		entry := make(map[string]interface{})
		descMap := structs.New(desc)
		for _, field := range tx.Statement.Schema.Fields {
			if field.DBName == "" {
				continue
			}
			if field.Tag.Get("shadow") == "ignore" {
				continue
			}
			entry[field.DBName] = descMap.Field(field.Name).Value()
		}

		// Create the shadow entry.
		if err := shadowDB.Table(desc.ShadowTable()).Create(entry).Error; err != nil {
			shadowDB.Logger.Error(
				shadowDB.Statement.Context,
				"Failed to create shadow entry: %v",
				err,
			)
			_ = shadowDB.AddError(err)
		}
	}
}

// BeforePreload alters the preloaded queries with the shadow table.
func (p *Plugin) BeforePreload(db *gorm.DB) {
	if db.Error != nil {
		return
	}

	stmt := db.Statement
	if stmt.Unscoped {
		return
	}

	desc, ok := stmt.Model.(Descriptor)
	if !ok || desc == nil {
		return
	}

	timestamp, err := p.TimeMachine.GetTime(stmt.Context)
	if err != nil || timestamp.IsZero() {
		return
	}

	handledPreloads := make(map[string]struct{})
	for preload, preloadConds := range stmt.Preloads {
		preloadPath := strings.Split(preload, ".")
		relations := stmt.Schema.Relationships.Relations
		var modelRefType reflect.Type

		// Find the model type of the last relationship
		for i, subModelName := range preloadPath {
			schema := relations[subModelName].FieldSchema
			modelRefType = schema.ModelType
			relations = schema.Relationships.Relations

			// Get the full path of this partial preload
			subPath := strings.Join(strings.Split(preload, ".")[:i+1], ".")

			nestDesc, ok := reflect.New(modelRefType).Interface().(Descriptor)
			if !ok {
				continue
			}

			// Handled by BeforeQuery
			if nestDesc.ShadowTable() == desc.ShadowTable() {
				continue
			}

			if _, ok := handledPreloads[subPath]; ok {
				continue
			}

			// Mark the preload as handled
			handledPreloads[subPath] = struct{}{}

			// Get the original, non-shadow, table name
			originalTable := stmt.NamingStrategy.TableName(modelRefType.Name())

			preloads := make([]interface{}, 0)

			// Load the preload conditions for the last relationship
			if subPath == preload {
				preloads = append(preloads, preloadConds...)
			}

			preloads = append([]interface{}{
				func(tx *gorm.DB) *gorm.DB {
					p.alterStatement(tx, timestamp, schema, nestDesc.ShadowTable(), originalTable)
					return tx
				},
			}, preloads...)

			db.Statement.Preloads[subPath] = preloads
		}
	}
}

// BeforeQuery alters the query with the shadow table.
func (p *Plugin) BeforeQuery(db *gorm.DB) {
	if db.Error != nil {
		return
	}

	stmt := db.Statement
	if stmt.Unscoped {
		return
	}

	desc, ok := stmt.Model.(Descriptor)
	if !ok || desc == nil {
		return
	}

	timestamp, err := p.TimeMachine.GetTime(stmt.Context)
	if err != nil || timestamp.IsZero() {
		return
	}

	p.alterStatement(db, timestamp, stmt.Schema, desc.ShadowTable(), stmt.Table)
}

// alterStatement alters the query to fetch the latest
// data from the shadow table based on the timestamp.
func (p *Plugin) alterStatement(
	tx *gorm.DB,
	timestamp time.Time,
	schema *schema.Schema,
	shadowTable, originalTable string,
) {
	if tx.Error != nil {
		return
	}

	stmt := tx.Statement
	if stmt == nil || stmt.Unscoped {
		return
	}

	// Use shadow table with original alias
	// to keep the FROM clause intact
	stmt.AddClause(clause.From{
		Tables: []clause.Table{
			{
				Name:  shadowTable,
				Alias: originalTable,
			},
		},
	})

	latestSeqDB := tx.
		Session(&gorm.Session{NewDB: true}).
		Table(shadowTable).
		Select("MAX(shadow_seq) as shadow_seq").
		Where("shadow_timestamp <= ?", timestamp).
		Group(strings.Join(schema.PrimaryFieldDBNames, ","))

	stmt.AddClause(clause.Where{
		Exprs: []clause.Expression{
			clause.NamedExpr{
				SQL:  "shadow_seq IN (?)",
				Vars: []interface{}{latestSeqDB},
			},
		},
	})
}
