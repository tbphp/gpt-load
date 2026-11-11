package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0032 = "0032_video_tasks"

type videoTask0032 struct {
	ID                string  `gorm:"type:varchar(40);primaryKey;not null"`
	AccessKeyID       uint    `gorm:"not null;default:0;uniqueIndex:idx_video_tasks_idempotency,priority:1"`
	GroupID           uint    `gorm:"not null;default:0"`
	CredentialID      uint    `gorm:"not null;default:0"`
	UpstreamTaskID    string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamTaskAlias string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamVideoID   string  `gorm:"type:varchar(255);not null;default:''"`
	UpstreamModel     string  `gorm:"type:varchar(255);not null;default:''"`
	ClientModel       string  `gorm:"type:varchar(255);not null;default:''"`
	Status            string  `gorm:"type:varchar(32);not null;default:'queued';check:chk_video_tasks_status,status IN ('queued','in_progress','completed','failed','cancelled')"`
	Mode              string  `gorm:"type:varchar(32);not null;default:'text';check:chk_video_tasks_mode,mode IN ('text','keyframe','reference')"`
	Seconds           string  `gorm:"type:varchar(8);not null;default:'5'"`
	Size              string  `gorm:"type:varchar(16);not null;default:'720P';check:chk_video_tasks_size,size IN ('720P','1080P','1K','2K')"`
	AspectRatio       string  `gorm:"type:varchar(16);not null;default:'16:9'"`
	ResultURL         string  `gorm:"type:text;not null;default:''"`
	LastErrorCode     string  `gorm:"type:varchar(64);not null;default:''"`
	LastErrorMessage  string  `gorm:"type:text;not null;default:''"`
	IdempotencyKey    *string `gorm:"type:varchar(64);uniqueIndex:idx_video_tasks_idempotency,priority:2"`
	Progress          int     `gorm:"not null;default:0;check:chk_video_tasks_progress,progress >= 0 AND progress <= 100"`
	CreatedAtMS       int64   `gorm:"column:created_at_ms;not null;default:0;index:idx_video_tasks_expires"`
	UpdatedAtMS       int64   `gorm:"column:updated_at_ms;not null;default:0"`
	ExpiresAtMS       int64   `gorm:"column:expires_at_ms;not null;default:0;index:idx_video_tasks_expires"`
}

func (videoTask0032) TableName() string { return "video_tasks" }

func Up0032(db *gorm.DB) error {
	if Validate0032(db) == nil {
		return nil
	}
	if err := ValidateRecoverable0032(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(&videoTask0032{}); err != nil {
		return fmt.Errorf("create video tasks: %w", err)
	}
	return Validate0032(db)
}

func ValidateRecoverable0032(db *gorm.DB) error {
	if !db.Migrator().HasTable(&videoTask0032{}) {
		return nil
	}
	if Validate0032(db) == nil {
		return nil
	}
	var count int64
	if err := db.Model(&videoTask0032{}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("incomplete video tasks schema contains data")
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&videoTask0032{}); err != nil {
		return err
	}
	columns, err := db.Migrator().ColumnTypes(&videoTask0032{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if statement.Schema.LookUpField(column.Name()) == nil {
			return fmt.Errorf("video tasks contains unexpected column %q", column.Name())
		}
	}
	return nil
}

func Validate0032(db *gorm.DB) error {
	model := &videoTask0032{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("video tasks table is missing")
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return err
	}
	columns, err := db.Migrator().ColumnTypes(model)
	if err != nil {
		return err
	}
	found := make(map[string]bool)
	for _, column := range columns {
		field := statement.Schema.LookUpField(column.Name())
		if field == nil {
			return fmt.Errorf("video tasks contains unexpected column %q", column.Name())
		}
		found[column.Name()] = true
		integer := field.DataType == "int" || field.DataType == "uint" || field.DataType == "int64"
		if integer && !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("video tasks column %s must be integer", column.Name())
		}
		if field.NotNull {
			if nullable, known := column.Nullable(); known && nullable {
				return fmt.Errorf("video tasks column %s must be non-null", column.Name())
			}
		}
	}
	for _, field := range statement.Schema.Fields {
		if !found[field.DBName] {
			return fmt.Errorf("video tasks column %s is missing", field.DBName)
		}
	}
	for _, index := range statement.Schema.ParseIndexes() {
		if !db.Migrator().HasIndex(model, index.Name) {
			return fmt.Errorf("video tasks index %s is missing", index.Name)
		}
	}
	for name := range statement.Schema.ParseCheckConstraints() {
		if !db.Migrator().HasConstraint(model, name) {
			return fmt.Errorf("video tasks constraint %s is missing", name)
		}
	}
	return nil
}
