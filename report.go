package main

import (
	"fmt"
	"time"
)

func human(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if f < 1024 {
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f PiB", f)
}

func printTable(now time.Time, loc *time.Location, selMonth string, rows []Row) {
	tnow := now.In(loc)
	fmt.Printf("per-peer traffic ledger (persistent, %s) - down=peer download, up=peer upload\n\n", tnow.Format("MST"))
	fmt.Printf("%-16s%-12s%-13s%-12s%-16s%-11s\n", "PEER", "ADDRESS", "TOTAL down", "TOTAL up", "MONTH "+selMonth, "TODAY")
	fmt.Println("------------------------------------------------------------------------------------------")
	if len(rows) == 0 {
		fmt.Println("(no data yet - the collector runs on a timer; check back after the first run)")
		return
	}
	for _, r := range rows {
		fmt.Printf("%-16s%-12s%-13s%-12s%-16s%-11s\n",
			r.Peer, r.IP,
			human(r.DownTotal), human(r.UpTotal),
			human(r.DownMonth+r.UpMonth), human(r.DownToday+r.UpToday))
	}
}

func relAgo(s int64) string {
	switch {
	case s < 0:
		return "now"
	case s < 60:
		return fmt.Sprintf("%ds ago", s)
	case s < 3600:
		return fmt.Sprintf("%dm ago", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh %dm ago", s/3600, (s%3600)/60)
	default:
		return fmt.Sprintf("%dd ago", s/86400)
	}
}
