// Package health interprets drive health data from smartctl.
//
// Vault only ever runs smartctl in read-only modes (-i, -H, -A). It never
// starts self-tests or changes drive settings.
package health

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Status is the simplified health verdict shown to users.
type Status string

const (
	Healthy  Status = "healthy"
	Warning  Status = "warning"
	Critical Status = "critical"
	Unknown  Status = "unknown"
)

// Thresholds used to raise a Warning. They are conservative on purpose:
// Vault never hides a problem, but also should not cry wolf.
const (
	warnTemperatureC   = 60
	warnNVMeUsedPct    = 90
	attrReallocated    = 5
	attrPending        = 197
	attrOfflineUncorr  = 198
	attrReportedUncorr = 187
)

// Report is the health information Vault displays for one drive.
type Report struct {
	Status       Status   `json:"status"`
	SmartPassed  *bool    `json:"smart_passed,omitempty"`
	TemperatureC *int     `json:"temperature_c,omitempty"`
	PowerOnHours *int64   `json:"power_on_hours,omitempty"`
	Reallocated  *int64   `json:"reallocated_sectors,omitempty"`
	Pending      *int64   `json:"pending_sectors,omitempty"`
	MediaErrors  *int64   `json:"media_errors,omitempty"`
	PercentUsed  *int     `json:"percent_used,omitempty"`
	Reasons      []string `json:"reasons,omitempty"`
	Message      string   `json:"message,omitempty"`
}

// UnknownReport returns a Report with status Unknown and the given reason.
func UnknownReport(msg string) Report {
	return Report{Status: Unknown, Message: msg}
}

type smartJSON struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
		Messages   []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	ATAAttributes *struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value int64 `json:"value"`
			} `json:"raw"`
			WhenFailed string `json:"when_failed"`
			Name       string `json:"name"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMeLog *struct {
		CriticalWarning int   `json:"critical_warning"`
		Temperature     int   `json:"temperature"`
		PercentageUsed  int   `json:"percentage_used"`
		MediaErrors     int64 `json:"media_errors"`
		PowerOnHours    int64 `json:"power_on_hours"`
	} `json:"nvme_smart_health_information_log"`
}

// smartctl exit status bits (see smartctl(8) "RETURN VALUES").
const (
	bitCmdLineError   = 1 << 0
	bitOpenFailed     = 1 << 1
	bitDiskFailing    = 1 << 3
	bitPrefailBelow   = 1 << 4
	bitErrorLogErrors = 1 << 6
)

// Parse interprets `smartctl -j -H -A -i` output.
func Parse(data []byte) (Report, error) {
	var s smartJSON
	if err := json.Unmarshal(data, &s); err != nil {
		return UnknownReport("SMART output could not be read"), fmt.Errorf("health: parse smartctl json: %w", err)
	}
	exit := s.Smartctl.ExitStatus
	if exit&(bitCmdLineError|bitOpenFailed) != 0 && s.SmartStatus == nil {
		msg := "SMART data unavailable"
		for _, m := range s.Smartctl.Messages {
			if m.Severity == "error" {
				msg = m.String
				break
			}
		}
		if strings.Contains(strings.ToLower(msg), "permission denied") {
			msg = "SMART data needs elevated access; Vault's privileged helper arrives in a later milestone"
		}
		return UnknownReport(msg), nil
	}

	r := Report{Status: Unknown}
	if s.SmartStatus != nil {
		passed := s.SmartStatus.Passed
		r.SmartPassed = &passed
	}
	if s.Temperature != nil && s.Temperature.Current > 0 {
		t := s.Temperature.Current
		r.TemperatureC = &t
	}
	if s.PowerOnTime != nil {
		h := s.PowerOnTime.Hours
		r.PowerOnHours = &h
	}
	if s.ATAAttributes != nil {
		for _, a := range s.ATAAttributes.Table {
			v := a.Raw.Value
			switch a.ID {
			case attrReallocated:
				r.Reallocated = &v
			case attrPending:
				r.Pending = &v
			}
			if v > 0 && (a.ID == attrOfflineUncorr || a.ID == attrReportedUncorr) {
				r.Reasons = append(r.Reasons, fmt.Sprintf("%s: %d", a.Name, v))
			}
			if a.WhenFailed == "now" {
				r.Reasons = append(r.Reasons, fmt.Sprintf("attribute %s is failing now", a.Name))
			}
		}
	}
	if n := s.NVMeLog; n != nil {
		me := n.MediaErrors
		r.MediaErrors = &me
		pu := n.PercentageUsed
		r.PercentUsed = &pu
		if r.TemperatureC == nil && n.Temperature > 0 {
			t := n.Temperature
			r.TemperatureC = &t
		}
		if r.PowerOnHours == nil {
			h := n.PowerOnHours
			r.PowerOnHours = &h
		}
		if n.CriticalWarning != 0 {
			r.Reasons = append(r.Reasons, fmt.Sprintf("NVMe critical warning flags 0x%02x", n.CriticalWarning))
		}
	}

	critical := false
	if r.SmartPassed != nil && !*r.SmartPassed {
		critical = true
		r.Reasons = append([]string{"SMART overall health check FAILED"}, r.Reasons...)
	}
	if exit&bitDiskFailing != 0 {
		critical = true
		r.Reasons = append(r.Reasons, "smartctl reports the disk is failing")
	}
	if exit&bitPrefailBelow != 0 {
		r.Reasons = append(r.Reasons, "a pre-failure attribute is below its threshold")
	}
	if exit&bitErrorLogErrors != 0 {
		r.Reasons = append(r.Reasons, "the drive error log contains errors")
	}
	if r.Reallocated != nil && *r.Reallocated > 0 {
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d reallocated sectors", *r.Reallocated))
	}
	if r.Pending != nil && *r.Pending > 0 {
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d sectors pending reallocation", *r.Pending))
	}
	if r.MediaErrors != nil && *r.MediaErrors > 0 {
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d media errors", *r.MediaErrors))
	}
	if r.PercentUsed != nil && *r.PercentUsed >= warnNVMeUsedPct {
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d%% of rated endurance used", *r.PercentUsed))
	}
	if r.TemperatureC != nil && *r.TemperatureC >= warnTemperatureC {
		r.Reasons = append(r.Reasons, fmt.Sprintf("running hot at %d°C", *r.TemperatureC))
	}

	switch {
	case critical:
		r.Status = Critical
	case len(r.Reasons) > 0:
		r.Status = Warning
	case r.SmartPassed != nil:
		r.Status = Healthy
	default:
		r.Message = "drive did not report an overall SMART verdict"
	}
	return r, nil
}
