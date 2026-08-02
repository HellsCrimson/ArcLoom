package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

func TestCostConfigEndpoints(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{})
	h := srv.Handler()

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p1/cost", strings.NewReader(body)))
		return rec
	}

	// Enabling with an override succeeds and persists (blank targets pruned).
	if rec := put(`{"enabled":true,"overrides":[{"target":"proxmox_vm","monthly":12},{"target":"  ","monthly":9}]}`); rec.Code != http.StatusOK {
		t.Fatalf("licensed set cost = %d (%s)", rec.Code, rec.Body.String())
	}
	if c := srv.settings.Cost("p1"); !c.Enabled || len(c.Overrides) != 1 || c.Overrides[0].Target != "proxmox_vm" {
		t.Fatalf("cost not stored / blank not pruned: %+v", c)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/cost", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"proxmox_vm"`) {
		t.Fatalf("get cost = %d (%s)", rec.Code, rec.Body.String())
	}

	// Disabling clears the flag and the overrides.
	if rec := put(`{"enabled":false,"overrides":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("disable = %d, want 200", rec.Code)
	}
	if c := srv.settings.Cost("p1"); c.Enabled || len(c.Overrides) != 0 {
		t.Fatalf("cost not cleared: %+v", c)
	}

	// Unknown project → 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope/cost", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d, want 404", rec.Code)
	}
}

func lineFor(c *runner.CostResult, addr string) (runner.CostLine, bool) {
	for _, l := range c.Lines {
		if l.Address == addr {
			return l, true
		}
	}
	return runner.CostLine{}, false
}

func TestBuildCostOverrideBeatsInfracost(t *testing.T) {
	changes := []runner.PlanChange{
		{Address: "aws_instance.web", Type: "aws_instance", Mode: "managed", Action: "create"},
		{Address: "proxmox_vm.db", Type: "proxmox_vm", Mode: "managed", Action: "create"},
	}
	overrides := []settings.CostOverride{
		{Target: "proxmox_vm", Monthly: 12}, // type override for a resource infracost can't price
		{Target: "aws_instance.web", Monthly: 5},
	}
	infracost := map[string]float64{"aws_instance.web": 140}

	cost := buildCost(changes, overrides, infracost, nil)

	if !cost.InfracostUsed {
		t.Fatal("expected InfracostUsed=true")
	}
	web, ok := lineFor(cost, "aws_instance.web")
	if !ok || web.Monthly != 5 || web.Source != "override" {
		t.Fatalf("address override should win over infracost: %+v", web)
	}
	db, ok := lineFor(cost, "proxmox_vm.db")
	if !ok || db.Monthly != 12 || db.Source != "override" {
		t.Fatalf("type override should price the homelab resource: %+v", db)
	}
	if cost.TotalMonthly != 17 {
		t.Fatalf("total = %v, want 17", cost.TotalMonthly)
	}
}

func TestBuildCostAddressOverrideBeatsTypeOverride(t *testing.T) {
	changes := []runner.PlanChange{
		{Address: "aws_instance.web", Type: "aws_instance", Mode: "managed", Action: "create"},
	}
	overrides := []settings.CostOverride{
		{Target: "aws_instance", Monthly: 100},
		{Target: "aws_instance.web", Monthly: 7},
	}
	cost := buildCost(changes, overrides, nil, nil)
	web, _ := lineFor(cost, "aws_instance.web")
	if web.Monthly != 7 {
		t.Fatalf("address match should beat type match: %+v", web)
	}
}

func TestBuildCostInfracostPricesNoOpResources(t *testing.T) {
	// No changes (steady state), but infracost prices the full planned state.
	infracost := map[string]float64{"aws_db_instance.main": 220}
	cost := buildCost(nil, nil, infracost, nil)
	line, ok := lineFor(cost, "aws_db_instance.main")
	if !ok || line.Source != "infracost" || line.Type != "aws_db_instance" {
		t.Fatalf("infracost-only resource should appear with derived type: %+v", line)
	}
	if cost.TotalMonthly != 220 {
		t.Fatalf("total = %v, want 220", cost.TotalMonthly)
	}
}

func TestBuildCostSkipsDataAndDeletes(t *testing.T) {
	changes := []runner.PlanChange{
		{Address: "data.aws_ami.x", Type: "aws_ami", Mode: "data", Action: "read"},
		{Address: "aws_instance.gone", Type: "aws_instance", Mode: "managed", Action: "delete"},
		{Address: "aws_instance.keep", Type: "aws_instance", Mode: "managed", Action: "create"},
	}
	overrides := []settings.CostOverride{{Target: "aws_instance", Monthly: 10}}
	cost := buildCost(changes, overrides, nil, nil)
	if len(cost.Lines) != 1 {
		t.Fatalf("expected only the kept managed resource, got %d lines: %+v", len(cost.Lines), cost.Lines)
	}
	if _, ok := lineFor(cost, "aws_instance.keep"); !ok {
		t.Fatal("kept resource missing")
	}
}

func TestBuildCostInfracostErrorDegradesToOverrides(t *testing.T) {
	changes := []runner.PlanChange{
		{Address: "proxmox_vm.db", Type: "proxmox_vm", Mode: "managed", Action: "create"},
	}
	overrides := []settings.CostOverride{{Target: "proxmox_vm", Monthly: 12}}
	cost := buildCost(changes, overrides, nil, errors.New("infracost not installed"))
	if cost.InfracostUsed {
		t.Fatal("InfracostUsed should be false on error")
	}
	if cost.Error != "infracost not installed" {
		t.Fatalf("error = %q", cost.Error)
	}
	if cost.TotalMonthly != 12 {
		t.Fatalf("overrides should still apply: total=%v", cost.TotalMonthly)
	}
}

func TestTypeFromAddress(t *testing.T) {
	cases := map[string]string{
		"aws_instance.web":           "aws_instance",
		"module.vpc.aws_subnet.a":    "aws_subnet",
		"module.a.module.b.res.name": "res",
		"data.aws_ami.x":             "aws_ami",
		"proxmox_vm.db":              "proxmox_vm",
	}
	for addr, want := range cases {
		if got := typeFromAddress(addr); got != want {
			t.Errorf("typeFromAddress(%q) = %q, want %q", addr, got, want)
		}
	}
}
