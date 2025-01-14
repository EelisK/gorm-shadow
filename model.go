package gormshadow

import (
	"time"
)

// Descriptor is an interface that represents a model that can be shadowed.
type Descriptor interface {
	ShadowTable() string
}

// Model is a struct that represents a point in time of a shadowed model.
type Model[T Descriptor] struct {
	Seq       uint      `gorm:"column:shadow_seq;primaryKey;autoIncrement"`
	Timestamp time.Time `gorm:"column:shadow_timestamp;index;default:now()"`
	Model     T         `gorm:"embedded"`
}

// TableName returns the name of the shadow table.
func (m *Model[T]) TableName() string {
	return m.Model.ShadowTable()
}
