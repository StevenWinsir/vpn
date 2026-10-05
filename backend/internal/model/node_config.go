package model

import "time"

type NodeConfig struct {
	NodeID     string `gorm:"type:uuid;primaryKey" json:"-"`
	Node       Node   `gorm:"constraint:OnDelete:CASCADE" json:"-"`
	Ciphertext string `gorm:"type:text;not null" json:"-"`
	PlanIDs    string `gorm:"type:text;not null;default:'[]'" json:"-"`
	UpdatedAt  time.Time
}

type AdminAudit struct {
	ID        string `gorm:"type:uuid;primaryKey"`
	ActorID   string `gorm:"type:uuid;not null;index"`
	Action    string `gorm:"size:40;not null"`
	TargetID  string `gorm:"type:uuid;not null;index"`
	Version   int64  `gorm:"not null"`
	CreatedAt time.Time
}
