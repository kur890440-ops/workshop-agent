package workshops_test

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/inventory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

func fixture(t *testing.T) (*workshops.Service, string, int64, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s := workshops.NewService(path)
	t.Cleanup(func() { s.Close() })
	owner := user(t, s, 100001)
	w, err := s.CreateOwnedWorkshop(owner, "Workshop A")
	must(t, err)
	return s, path, owner, w
}
func user(t *testing.T, s *workshops.Service, telegram int64) int64 {
	t.Helper()
	u, err := s.UpsertUser(telegram, "name", "Person", "")
	must(t, err)
	return u
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func denied(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected access denied")
	}
}
func member(t *testing.T, s *workshops.Service, owner, w, telegram int64, role auth.Role) int64 {
	t.Helper()
	u := user(t, s, telegram)
	_, token, err := s.CreateInvite(owner, w, role, 0, 0)
	must(t, err)
	_, err = s.AcceptInvite(u, token)
	must(t, err)
	return u
}

func TestUsersWorkshopsInvitesAndPermissions(t *testing.T) {
	s, path, owner, w := fixture(t)
	same, err := s.UpsertUser(100001, "changed_username", "New", "Name")
	must(t, err)
	if same != owner || owner == 100001 {
		t.Fatal("external and internal identity confused")
	}
	active, err := s.ActiveWorkshop(owner)
	must(t, err)
	if active != w {
		t.Fatal("owner context missing")
	}
	m, err := s.Members(owner, w)
	must(t, err)
	if len(m) != 1 || m[0].Role != auth.Owner {
		t.Fatal("creator not owner")
	}
	employee := member(t, s, owner, w, 100002, auth.Employee)
	viewer := member(t, s, owner, w, 100003, auth.Viewer)
	admin := member(t, s, owner, w, 100004, auth.Admin)
	for _, c := range []struct {
		u  int64
		p  auth.Permission
		ok bool
	}{{viewer, auth.InventoryRead, true}, {viewer, auth.InventoryWrite, false}, {employee, auth.ProductionCreate, true}, {employee, auth.MembersManage, false}, {admin, auth.MembersInvite, true}, {admin, auth.OwnershipTransfer, false}, {owner, auth.WorkshopDelete, true}} {
		err := auth.Require(s.DB(), c.u, w, c.p)
		if (err == nil) != c.ok {
			t.Fatalf("user=%d permission=%s: %v", c.u, c.p, err)
		}
	}
	inv := inventory.NewService(path)
	defer inv.Close()
	_, err = inv.ForUser(viewer).CreateMaterial(w, "bad", "raw", "g", 1, 0, "", 0, "")
	denied(t, err)
	_, err = inv.ForUser(employee).CreateMaterial(w, "allowed", "raw", "g", 1, 0, "", 0, "")
	must(t, err)
	_, err = inv.ListMaterials(w)
	denied(t, err) // An unbound service cannot bypass authorization.
	_, err = inv.ForUser(viewer).ListMaterials(w)
	must(t, err)
	for _, r := range []auth.Role{auth.Owner, "owner", "unknown"} {
		_, _, err = s.CreateInvite(admin, w, r, 0, 0)
		denied(t, err)
	}
	must(t, s.SetUserSettings(employee, `{"language":"ru"}`))
	denied(t, s.SetWorkshopSettings(employee, w, `{}`))
	must(t, s.SetWorkshopSettings(admin, w, `{"units":"g"}`))
}

func TestInvitePolicyAndDuplicateMembership(t *testing.T) {
	s, _, owner, w := fixture(t)
	u := user(t, s, 100002)
	invite, token, err := s.CreateInvite(owner, w, auth.Employee, 0, 0)
	must(t, err)
	if len(token) != 43 || invite.MaxUses != 1 || invite.ExpiresAt-time.Now().Unix() < 86395 {
		t.Fatal("wrong default invite policy")
	}
	var hash string
	must(t, s.DB().QueryRow(`SELECT token_hash FROM workshop_invites WHERE id=?`, invite.ID).Scan(&hash))
	if hash == token || len(hash) != 64 {
		t.Fatal("raw token stored")
	}
	before, err := s.Members(owner, w)
	must(t, err)
	_, err = s.PreviewInvite(token)
	must(t, err)
	after, err := s.Members(owner, w)
	must(t, err)
	if len(before) != len(after) {
		t.Fatal("preview joined automatically")
	}
	_, err = s.AcceptInvite(u, token)
	must(t, err)
	other := user(t, s, 100003)
	_, err = s.AcceptInvite(other, token)
	denied(t, err)
	_, err = s.AcceptInvite(u, token)
	denied(t, err)
	_, token2, err := s.CreateInvite(owner, w, auth.Admin, 0, 2)
	must(t, err)
	_, err = s.AcceptInvite(u, token2)
	if !errors.Is(err, workshops.ErrMemberExists) {
		t.Fatal(err)
	}
	p, err := s.PreviewInvite(token2)
	must(t, err)
	if p.UsedCount != 0 {
		t.Fatal("duplicate spent an invite")
	}
	_, err = s.AcceptInvite(other, token2)
	must(t, err)
	must(t, s.RevokeInvite(owner, w, p.ID))
	_, err = s.AcceptInvite(user(t, s, 100004), token2)
	denied(t, err)
	expired, token3, err := s.CreateInvite(owner, w, auth.Viewer, 0, 0)
	must(t, err)
	_, err = s.DB().Exec(`UPDATE workshop_invites SET expires_at=? WHERE id=?`, time.Now().Unix()-1, expired.ID)
	must(t, err)
	_, err = s.PreviewInvite(token3)
	denied(t, err)
	_, err = s.AcceptInvite(user(t, s, 100005), token3)
	denied(t, err)
	list, err := s.Invites(owner, w)
	must(t, err)
	if list[0].Status != "expired" {
		t.Fatal("expired state not shown")
	}
	var raw string
	must(t, s.DB().QueryRow(`SELECT group_concat(metadata_json || details,' ') FROM audit_logs`).Scan(&raw))
	for _, secret := range []string{token, token2, token3, hash} {
		if strings.Contains(raw, secret) {
			t.Fatal("invite secret leaked to audit")
		}
	}
}

func TestInviteConcurrentAcceptance(t *testing.T) {
	s, path, owner, w := fixture(t)
	second := workshops.NewService(path)
	defer second.Close()
	u1 := user(t, s, 100002)
	u2 := user(t, s, 100003)
	_, token, err := s.CreateInvite(owner, w, auth.Employee, 0, 0)
	must(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, v := range []struct {
		s *workshops.Service
		u int64
	}{{s, u1}, {second, u2}} {
		wg.Add(1)
		go func(v struct {
			s *workshops.Service
			u int64
		}) {
			defer wg.Done()
			<-start
			_, err := v.s.AcceptInvite(v.u, token)
			results <- err
		}(v)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, workshops.ErrInvite) {
			t.Fatalf("losing accept should observe consumed invite: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("success count=%d", success)
	}
	var count int
	must(t, s.DB().QueryRow(`SELECT COUNT(*) FROM workshop_members WHERE workshop_id=?`, w).Scan(&count))
	if count != 2 {
		t.Fatal("one-use invite admitted multiple members")
	}
}

func TestIsolationAndActiveContext(t *testing.T) {
	s, path, owner, w := fixture(t)
	outsider := user(t, s, 200001)
	other, err := s.CreateOwnedWorkshop(outsider, "Workshop B")
	must(t, err)
	inv := inventory.NewService(path)
	defer inv.Close()
	prod := products.NewBOMService(path)
	defer prod.Close()
	mat, err := inv.ForUser(outsider).CreateMaterial(other, "secret", "raw", "g", 4, 1, "", 0, "")
	must(t, err)
	product, err := prod.ForUser(outsider).CreateProduct(other, "secret", "", "product", 0, 0, "")
	must(t, err)
	_, err = inv.ForUser(owner).ListMaterials(other)
	denied(t, err)
	_, err = prod.ForUser(owner).ListProducts(other)
	denied(t, err)
	_, err = prod.ForUser(owner).GetBOM(other, product)
	denied(t, err)
	_, err = s.Members(owner, other)
	denied(t, err)
	_, err = s.Invites(owner, other)
	denied(t, err)
	denied(t, s.SetActiveWorkshop(owner, other))
	local, err := prod.ForUser(owner).CreateProduct(w, "local", "", "product", 0, 0, "")
	must(t, err)
	denied(t, prod.ForUser(owner).SetBOMItem(w, local, "material", mat, 0, 1, "g", 0, ""))
	denied(t, prod.ForUser(owner).SetBOMItem(w, local, "product", 0, product, 1, "pcs", 0, ""))
	denied(t, inv.ForUser(owner).CreateMaterialStockMovement(w, mat, 1, "test", "", 0, owner, ""))
	_, token, err := s.CreateInvite(outsider, other, auth.Employee, 0, 0)
	must(t, err)
	_, err = s.AcceptInvite(owner, token)
	must(t, err)
	must(t, s.SetActiveWorkshop(owner, w))
	active, err := s.ActiveWorkshop(owner)
	must(t, err)
	if active != w {
		t.Fatal("switch failed")
	}
	must(t, s.SetActiveWorkshop(owner, other))
	must(t, s.ChangeMemberStatus(outsider, other, owner, "disabled"))
	active, err = s.ActiveWorkshop(owner)
	if active != 0 || !errors.Is(err, auth.ErrChooseWorkshop) {
		t.Fatal("stale context retained")
	}
	must(t, s.SetActiveWorkshop(owner, w))
	_, err = inv.ForUser(owner).ListMaterials(other)
	denied(t, err)
}

func TestMembershipOwnerProtectionAndAudit(t *testing.T) {
	s, path, owner, w := fixture(t)
	admin := member(t, s, owner, w, 100002, auth.Admin)
	employee := member(t, s, owner, w, 100003, auth.Employee)
	for _, status := range []string{"disabled", "removed", "left"} {
		denied(t, s.ChangeMemberStatus(owner, w, owner, status))
		denied(t, s.ChangeMemberStatus(admin, w, owner, status))
	}
	denied(t, s.ChangeRole(owner, w, owner, auth.Admin))
	denied(t, s.ChangeRole(admin, w, employee, auth.Owner))
	denied(t, s.TransferOwnership(w, admin, employee))
	must(t, s.ChangeRole(admin, w, employee, auth.Viewer))
	must(t, s.ChangeRole(admin, w, employee, auth.Employee))
	inv := inventory.NewService(path)
	defer inv.Close()
	bound := inv.ForUser(employee)
	_, err := bound.ListMaterials(w)
	must(t, err)
	must(t, s.ChangeMemberStatus(admin, w, employee, "disabled"))
	_, err = bound.ListMaterials(w)
	if !errors.Is(err, auth.ErrDisabled) {
		t.Fatal("cached service retained access")
	}
	_, token, err := s.CreateInvite(owner, w, auth.Admin, 0, 0)
	must(t, err)
	_, err = s.AcceptInvite(employee, token)
	denied(t, err)
	must(t, s.ChangeMemberStatus(admin, w, employee, "active"))
	_, err = s.DB().Exec(`INSERT INTO production_records(workshop_id,product_id,attempted_quantity,good_quantity,scrap_quantity,user_id) VALUES(?,1,5,4,1,?)`, w, employee)
	must(t, err)
	must(t, s.ChangeMemberStatus(admin, w, employee, "removed"))
	var exists int
	must(t, s.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, employee).Scan(&exists))
	if exists != 1 {
		t.Fatal("user was deleted")
	}
	must(t, s.DB().QueryRow(`SELECT COUNT(*) FROM production_records WHERE workshop_id=? AND user_id=? AND good_quantity=4`, w, employee).Scan(&exists))
	if exists != 1 {
		t.Fatal("historical production author lost")
	}
	must(t, s.ChangeMemberStatus(admin, w, employee, "active"))
	must(t, s.ChangeMemberStatus(employee, w, employee, "left"))
	must(t, s.TransferOwnership(w, owner, admin))
	must(t, auth.Require(s.DB(), admin, w, auth.OwnershipTransfer))
	denied(t, auth.Require(s.DB(), owner, w, auth.OwnershipTransfer))
	for _, kind := range []string{"WORKSHOP_CREATED", "MEMBER_INVITED", "INVITE_ACCEPTED", "MEMBER_ROLE_CHANGED", "MEMBER_DISABLED", "MEMBER_ENABLED", "MEMBER_REMOVED", "MEMBER_LEFT", "OWNERSHIP_TRANSFERRED", "ACTIVE_WORKSHOP_CHANGED"} {
		var n int
		must(t, s.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE event_type=? AND workshop_id=?`, kind, w).Scan(&n))
		if n < 1 {
			t.Fatalf("audit event missing: %s", kind)
		}
	}
}

func TestManagementTransactionsRollbackWithAudit(t *testing.T) {
	s, _, owner, w := fixture(t)
	other := member(t, s, owner, w, 100002, auth.Employee)
	_, err := s.DB().Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	must(t, err)
	denied(t, s.TransferOwnership(w, owner, other))
	must(t, auth.Require(s.DB(), owner, w, auth.OwnershipTransfer))
	denied(t, auth.Require(s.DB(), other, w, auth.OwnershipTransfer))
}
