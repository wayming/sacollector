//go:build ignore

package main

import (
	"fmt"
	"os"
	"time"

	"sacollector/internal/exporter"
	"sacollector/internal/parser"
)

func main() {
	files := []string{"balance-sheet", "cash-flow-statement", "income-statement", "ratios"}

	for _, st := range files {
		path := fmt.Sprintf("output/raw/hkg/0700/%s_quarterly.json", st)
		raw, _ := os.ReadFile(path)
		t0 := time.Now()
		res, err := parser.ParseFinancial(raw)
		elapsed := time.Since(t0)
		if err != nil {
			fmt.Printf("%s: %v\n", st, err)
			continue
		}
		fmt.Printf("%-20s parse=%v  file=%dKB  dates=%d  metrics=%d\n",
			st, elapsed, len(raw)/1024, len(res.DateKeys), len(res.Financials))
	}

	// Export benchmark
	exp := exporter.New("output")
	statements := make(map[string]*parser.ResolvedFinancial)
	for _, st := range files {
		path := fmt.Sprintf("output/raw/hkg/0700/%s_quarterly.json", st)
		raw, _ := os.ReadFile(path)
		res, _ := parser.ParseFinancial(raw)
		statements[st] = res
	}
	t0 := time.Now()
	exp.ExportFinancial("0700", statements)
	fmt.Printf("export: %v\n", time.Since(t0))
}
