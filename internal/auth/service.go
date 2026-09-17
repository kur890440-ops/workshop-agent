// Package auth owns the role policy. IDs here are internal database IDs.
package auth

import (
	"database/sql"
	"errors"
	"sort"
)

type Role string
type Permission string

const (
	Owner             Role       = "OWNER"
	Admin             Role       = "ADMIN"
	Employee          Role       = "EMPLOYEE"
	Viewer            Role       = "VIEWER"
	WorkshopRead      Permission = "workshop.read"
	WorkshopManage    Permission = "workshop.settings.manage"
	WorkshopDelete    Permission = "workshop.delete"
	OwnershipTransfer Permission = "ownership.transfer"
	MembersRead       Permission = "members.read"
	MembersInvite     Permission = "members.invite"
	MembersManage     Permission = "members.manage"
	InventoryRead     Permission = "inventory.read"
	InventoryWrite    Permission = "inventory.write"
	ProductsRead      Permission = "products.read"
	ProductsWrite     Permission = "products.write"
	BOMRead           Permission = "bom.read"
	BOMWrite          Permission = "bom.write"
	ProductionRead    Permission = "production.read"
	ProductionCreate  Permission = "production.create"
	ProductionUpdate  Permission = "production.update"
	ShipmentsRead     Permission = "shipments.read"
	ShipmentsWrite    Permission = "shipments.write"
	PlanningRead      Permission = "planning.read"
	PlanningWrite     Permission = "planning.write"
	AuditRead         Permission = "audit.read"
)

var ErrDenied = errors.New("У вас нет прав для этой операции. Обратитесь к владельцу или администратору мастерской.")
var ErrDisabled = errors.New("Ваш доступ к мастерской отключён.")
var ErrChooseWorkshop = errors.New("Выберите доступную мастерскую.")
var all = []Permission{WorkshopRead, WorkshopManage, WorkshopDelete, OwnershipTransfer, MembersRead, MembersInvite, MembersManage, InventoryRead, InventoryWrite, ProductsRead, ProductsWrite, BOMRead, BOMWrite, ProductionRead, ProductionCreate, ProductionUpdate, ShipmentsRead, ShipmentsWrite, PlanningRead, PlanningWrite, AuditRead}
var read = []Permission{WorkshopRead, MembersRead, InventoryRead, ProductsRead, BOMRead, ProductionRead, ShipmentsRead, PlanningRead}

// Permissions returns a copy so callers cannot mutate policy.
func Permissions(role Role) []Permission {
	var out []Permission
	switch role {
	case Owner:
		out = append(out, all...)
	case Admin:
		for _, p := range all {
			if p != WorkshopDelete && p != OwnershipTransfer {
				out = append(out, p)
			}
		}
	case Employee:
		out = append(out, read...)
		out = append(out, InventoryWrite, ProductionCreate, ProductionUpdate, ShipmentsWrite)
	case Viewer:
		out = append(out, read...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func Has(role Role, p Permission) bool {
	for _, allowed := range Permissions(role) {
		if allowed == p {
			return true
		}
	}
	return false
}
func Assignable(role Role) bool { return role == Admin || role == Employee || role == Viewer }

type Querier interface{ QueryRow(string, ...any) *sql.Row }

// Require accepts a DB or transaction to authorize management atomically.
func Require(q Querier, userID, workshopID int64, permission Permission) error {
	if userID <= 0 || workshopID <= 0 {
		return ErrDenied
	}
	var role Role
	var status string
	var active int
	err := q.QueryRow(`SELECT m.role, m.status, m.is_active FROM workshop_members m JOIN users u ON u.id=m.user_id JOIN workshops w ON w.id=m.workshop_id WHERE m.user_id=? AND m.workshop_id=? AND u.status='active' AND u.is_active=1 AND w.status='active' AND w.is_active=1`, userID, workshopID).Scan(&role, &status, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	if status == "disabled" {
		return ErrDisabled
	}
	if status != "active" || active != 1 || !Has(role, permission) {
		return ErrDenied
	}
	return nil
}

type Service struct{ DB *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{DB: db} }
func (s *Service) RequirePermission(userID, workshopID int64, p Permission) error {
	return Require(s.DB, userID, workshopID, p)
}
