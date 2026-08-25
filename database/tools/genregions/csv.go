package main

import (
	"bufio"
	"encoding/csv"
	"os"
)

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return csv.NewReader(bufio.NewReader(f)).ReadAll()
}
