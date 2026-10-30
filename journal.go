package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type QueueItem struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Update    bool     `json:"update,omitempty"`
	Status    string   `json:"status"`
	Phase     string   `json:"phase,omitempty"`
	FilesDone []string `json:"files_done,omitempty"`
}

type Journal struct {
	Operation string      `json:"operation"`
	Txid      string      `json:"txid"`
	Started   string      `json:"started"`
	Queue     []QueueItem `json:"queue"`
}

func journalPath() string { return filepath.Join(dbDir, "journal.json") }

func newJournal(op string, queue []QueueItem) (*Journal, error) {
	j := &Journal{
		Operation: op,
		Txid:      time.Now().Format("20060102-150405"),
		Started:   time.Now().UTC().Format(time.RFC3339),
		Queue:     queue,
	}
	return j, j.save()
}

func loadJournal() (*Journal, error) {
	data, err := os.ReadFile(journalPath())
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (j *Journal) save() error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(journalPath(), data)
}

func (j *Journal) remove() { os.Remove(journalPath()) }

func (j *Journal) setPhase(i int, phase string) error {
	j.Queue[i].Phase = phase
	j.Queue[i].Status = "in_progress"
	return j.save()
}

func (j *Journal) setStatus(i int, status string) error {
	j.Queue[i].Status = status
	return j.save()
}

func (j *Journal) setFilesDone(i int, files []string) error {
	j.Queue[i].FilesDone = files
	return j.save()
}
