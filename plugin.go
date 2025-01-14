package gormshadow

import (
	"strings"
	"time"

	"github.com/fatih/structs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const (
	gormCommit = "gorm:commit_or_rollback_transaction"

	shadowCreate = "shadow:create"
	shadowUpdate = "shadow:update"
	shadowDelete = "shadow:delete"
)

// Plugin creates shadow entries for models that implement the Descriptor interface.
type Plugin struct{}

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
