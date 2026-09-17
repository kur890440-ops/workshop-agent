package models

type Workshop struct {
	ID        int64
	Name      string
	CreatedAt string
	IsActive  bool
	UpdatedAt string
	Status    string
}

type User struct {
	ID               int64
	TelegramUserID   int64
	TelegramUserName string
	DisplayName      string
	CreatedAt        string
	IsActive         bool
	FirstName        *string
	LastName         *string
	UpdatedAt        string
	Status           string
}

type WorkshopMembership struct {
	ID              int64
	WorkshopID      int64
	UserID          int64
	Role            string
	IsActive        bool
	Status          string
	CreatedAt       string
	UpdatedAt       string
	InvitedByUserID *int64
	JoinedAt        *string
}

// WorkshopMember is the legacy name of the same entity.
type WorkshopMember = WorkshopMembership

type UserWorkshopContext struct {
	UserID           int64
	ActiveWorkshopID *int64
	UpdatedAt        string
}

type TelegramChat struct {
	ID             int64
	TelegramChatID int64
	WorkshopID     int64
	Title          string
	IsActive       bool
}

type Material struct {
	ID           int64
	WorkshopID   int64
	Name         string
	Category     string
	BaseUnit     string
	CurrentStock float64
	MinimumStock float64
	Supplier     string
	LeadTimeDays int
	Notes        string
	CreatedAt    string
	UpdatedAt    string
}

type Product struct {
	ID           int64
	WorkshopID   int64
	Name         string
	SKU          string
	ProductType  string
	CurrentStock float64
	MinimumStock float64
	Notes        string
	CreatedAt    string
}

type BOMItem struct {
	ID                   int64
	WorkshopID           int64
	ProductID            int64
	ComponentType        string
	MaterialID           int64
	ComponentProductID   int64
	Quantity             float64
	Unit                 string
	TechnicalLossPercent float64
	Notes                string
}

type ProductionRecord struct {
	ID                int64
	WorkshopID        int64
	ProductID         int64
	Date              string
	AttemptedQuantity float64
	GoodQuantity      float64
	ScrapQuantity     float64
	UserID            int64
	Notes             string
	CreatedAt         string
}

type Shipment struct {
	ID              int64
	WorkshopID      int64
	ProductID       int64
	Quantity        float64
	Channel         string
	Date            string
	ExternalOrderID string
	UserID          int64
	Notes           string
}

type ProductionPlan struct {
	ID              int64
	WorkshopID      int64
	ProductID       int64
	PlannedQuantity float64
	StartDate       string
	DueDate         string
	Status          string
	Notes           string
}

type ConversationSession struct {
	ID             int64
	WorkshopID     int64
	TelegramChatID int64
	UserID         int64
	LastContext    string
	PendingAction  string
	UpdatedAt      string
}

type PendingAction struct {
	ID         int64
	WorkshopID int64
	ChatID     int64
	UserID     int64
	ActionType string
	Payload    string
	CreatedAt  string
	ExpiresAt  string
}

type InventoryMovement struct {
	ID            int64
	WorkshopID    int64
	MaterialID    int64
	Date          string
	Quantity      float64
	MovementType  string
	ReferenceType string
	ReferenceID   int64
	UserID        int64
	Comment       string
}

type ProductMovement struct {
	ID            int64
	WorkshopID    int64
	ProductID     int64
	Date          string
	Quantity      float64
	MovementType  string
	ReferenceType string
	ReferenceID   int64
	UserID        int64
	Comment       string
}
