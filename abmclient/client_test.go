package abmclient

import (
	"testing"
	"time"

	"github.com/zchee/abm"
)

func TestDevice_EmbeddedOrgDevice(t *testing.T) {
	d := Device{
		OrgDevice: abm.OrgDevice{
			ID: "DEV001",
			Attributes: &abm.OrgDeviceAttributes{
				SerialNumber:  "TESTSERIAL1",
				DeviceModel:   "MacBook Pro (16-inch, 2024)",
				ProductType:   "Mac16,1",
				Color:         "SILVER",
				ProductFamily: abm.ProductFamilyMac,
			},
		},
		AssignedServer: "TestMDM",
	}

	if d.ID != "DEV001" {
		t.Errorf("ID = %q, want DEV001", d.ID)
	}
	if d.Attributes.SerialNumber != "TESTSERIAL1" {
		t.Errorf("SerialNumber = %q", d.Attributes.SerialNumber)
	}
	if d.AssignedServer != "TestMDM" {
		t.Errorf("AssignedServer = %q", d.AssignedServer)
	}
}

func TestDevice_NilAttributes(t *testing.T) {
	d := Device{OrgDevice: abm.OrgDevice{ID: "DEV002"}}
	if d.Attributes != nil {
		t.Error("expected nil attributes")
	}
	if d.ID != "DEV002" {
		t.Errorf("ID = %q, want DEV002", d.ID)
	}
}

func TestAppleCareCoverage_Fields(t *testing.T) {
	start := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 6, 15, 0, 0, 0, 0, time.UTC)

	ac := AppleCareCoverage{
		AgreementNumber: "AGR-TEST-001",
		Description:     "AppleCare+ for Mac",
		StartDateTime:   start,
		EndDateTime:     end,
		Status:          "ACTIVE",
		PaymentType:     "Paid_up_front",
		IsRenewable:     true,
		IsCanceled:      false,
	}

	if ac.AgreementNumber != "AGR-TEST-001" {
		t.Errorf("AgreementNumber = %q", ac.AgreementNumber)
	}
	if ac.Status != "ACTIVE" {
		t.Errorf("Status = %q", ac.Status)
	}
	if !ac.IsRenewable {
		t.Error("IsRenewable should be true")
	}
	if ac.IsCanceled {
		t.Error("IsCanceled should be false")
	}
	if ac.EndDateTime != end {
		t.Errorf("EndDateTime = %v, want %v", ac.EndDateTime, end)
	}
}

// selectBestCoverage is the selection logic extracted for testing.
// It mirrors the loop in GetAppleCareCoverage.
func selectBestCoverage(records []AppleCareCoverage) *AppleCareCoverage {
	var best *AppleCareCoverage
	for i := range records {
		a := &records[i]
		if best == nil {
			best = a
			continue
		}
		bestActive := best.Status == "ACTIVE"
		aActive := a.Status == "ACTIVE"
		if aActive && !bestActive {
			best = a
			continue
		}
		if bestActive && !aActive {
			continue
		}
		bestPaid := best.PaymentType == "PAID_UP_FRONT"
		aPaid := a.PaymentType == "PAID_UP_FRONT"
		if aPaid && !bestPaid {
			best = a
			continue
		}
		if bestPaid && !aPaid {
			continue
		}
		if a.EndDateTime.After(best.EndDateTime) {
			best = a
		}
	}
	return best
}

func TestSelectBestCoverage_ActiveBeatsInactive(t *testing.T) {
	inactive := AppleCareCoverage{Status: "INACTIVE", EndDateTime: time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)}
	active := AppleCareCoverage{Status: "ACTIVE", EndDateTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	best := selectBestCoverage([]AppleCareCoverage{inactive, active})
	if best.Status != "ACTIVE" {
		t.Errorf("expected ACTIVE to win, got %q", best.Status)
	}
}

func TestSelectBestCoverage_TwoActive_PaidBeatsFree(t *testing.T) {
	// Models GDKQ1VCX93: two ACTIVE warranties, one paid, one not
	free := AppleCareCoverage{Status: "ACTIVE", PaymentType: "NONE", EndDateTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	paid := AppleCareCoverage{Status: "ACTIVE", PaymentType: "PAID_UP_FRONT", EndDateTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	best := selectBestCoverage([]AppleCareCoverage{free, paid})
	if best.PaymentType != "PAID_UP_FRONT" {
		t.Errorf("expected PAID_UP_FRONT to win, got %q", best.PaymentType)
	}
}

func TestSelectBestCoverage_TwoActive_SamePayment_LaterEndWins(t *testing.T) {
	earlier := AppleCareCoverage{Status: "ACTIVE", PaymentType: "PAID_UP_FRONT", EndDateTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	later := AppleCareCoverage{Status: "ACTIVE", PaymentType: "PAID_UP_FRONT", EndDateTime: time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)}
	best := selectBestCoverage([]AppleCareCoverage{earlier, later})
	if !best.EndDateTime.Equal(later.EndDateTime) {
		t.Errorf("expected later end date to win, got %v", best.EndDateTime)
	}
}

func TestSelectBestCoverage_Empty(t *testing.T) {
	best := selectBestCoverage(nil)
	if best != nil {
		t.Errorf("expected nil for empty input, got %+v", best)
	}
}

func TestSetLogLevel(t *testing.T) {
	// Just verify it doesn't panic
	SetLogLevel(0) // PanicLevel
	SetLogLevel(6) // TraceLevel
}
