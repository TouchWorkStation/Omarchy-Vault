package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
)

func row(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %-12s %s\n", label, value)
}

// humanBytes uses decimal units, matching how drives are sold ("8 TB").
func humanBytes(n uint64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	suffix := "KMGTPE"[exp : exp+1]
	if v >= 100 {
		return fmt.Sprintf("%.0f %sB", v, suffix)
	}
	return fmt.Sprintf("%.1f %sB", v, suffix)
}

func statusLabel(d disks.Disk) string {
	switch d.Status {
	case disks.StatusSystem:
		return "SYSTEM · PROTECTED"
	case disks.StatusAvailable:
		return "AVAILABLE"
	case disks.StatusUnmounted:
		return "NOT MOUNTED"
	case disks.StatusReadOnly:
		return "READ-ONLY"
	default:
		return "UNSUPPORTED"
	}
}

func printInventory(w io.Writer, inv *disks.Inventory) {
	fmt.Fprintln(w, "STORAGE")
	fmt.Fprintln(w)
	for _, d := range inv.Disks {
		fmt.Fprintf(w, "%-30s %9s  %-20s health: %s\n", d.DisplayName, humanBytes(uint64(d.SizeBytes)), statusLabel(d), d.Health.Status)
		fmt.Fprintf(w, "  %s · %s", d.Path, d.Kind)
		if d.Removable {
			fmt.Fprint(w, " · removable")
		}
		fmt.Fprintln(w)
		if d.ProtectedReason != "" {
			fmt.Fprintf(w, "  %s\n", d.ProtectedReason)
		}
		for _, v := range d.Volumes {
			mp := "—"
			if len(v.Mountpoints) > 0 {
				mp = strings.Join(v.Mountpoints, ", ")
				if len(mp) > 48 {
					mp = mp[:45] + "..."
				}
			}
			fs := v.FSType
			if fs == "" {
				fs = "none"
			}
			usage := ""
			if v.FSSize > 0 {
				usage = fmt.Sprintf("  %s free of %s", humanBytes(uint64(v.FSAvail)), humanBytes(uint64(v.FSSize)))
			}
			fmt.Fprintf(w, "    %-16s %-8s %-11s %s%s\n", v.Name, fs, v.Status, mp, usage)
			for _, n := range v.Notes {
				fmt.Fprintf(w, "      %s\n", n)
			}
		}
		if d.Health.Message != "" && d.Health.Status == "unknown" {
			fmt.Fprintf(w, "  health: %s\n", d.Health.Message)
		}
		for _, r := range d.Health.Reasons {
			fmt.Fprintf(w, "  ! %s\n", r)
		}
		for _, n := range d.Notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
		fmt.Fprintln(w)
	}
	s := inv.Summary
	fmt.Fprintf(w, "%d drives · %d system (protected) · %d available · %d not mounted · %d unsupported\n",
		s.Total, s.System, s.Available, s.Unmounted, s.Unsupported)
	for _, warn := range inv.Warnings {
		fmt.Fprintf(w, "! %s\n", warn)
	}
	fmt.Fprintln(w, "\nVault never formats, partitions or erases drives.")
}

func printShortcuts(w io.Writer, rep shortcuts.Report) {
	fmt.Fprintln(w, "SHORTCUTS")
	fmt.Fprintln(w)
	for _, c := range rep.Checks {
		label := c.Label
		if c.Direction != "" {
			label += " (" + c.Direction + ")"
		}
		fmt.Fprintf(w, "%-22s %-40s %s\n", c.Combo, label, strings.ToUpper(c.Status))
		if c.Status == "conflict" {
			fmt.Fprintf(w, "\n  Shortcut Conflict\n  %s is already assigned:\n", c.Combo)
			for _, b := range c.Conflicts {
				what := b.Description
				if what == "" {
					what = strings.TrimSpace(b.Dispatcher + " " + b.Arg)
				}
				fmt.Fprintf(w, "    %s  (%s)\n", what, b.Source)
			}
			fmt.Fprintln(w, "  Options:")
			if c.Suggestion != nil {
				fmt.Fprintf(w, "    - Choose Another Shortcut   e.g. %s\n", c.Suggestion.Combo())
			} else {
				fmt.Fprintln(w, "    - Choose Another Shortcut")
			}
			fmt.Fprintf(w, "    - Copy Binding Command      %s\n", c.Line)
			fmt.Fprintln(w, "    - Skip Shortcut")
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w)
	if len(rep.Sources) > 0 {
		fmt.Fprintf(w, "Checked: %s\n", strings.Join(rep.Sources, ", "))
	}
	for _, warn := range rep.Warnings {
		fmt.Fprintf(w, "! %s\n", warn)
	}
	fmt.Fprintln(w, "Install the free ones with: vaultctl shortcuts install   (existing shortcuts are never overwritten)")
}
