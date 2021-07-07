package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fatih/color"
	"github.com/rogpeppe/go-internal/lockedfile"
)

type Task struct {
	ID     int64  `json:"id"`
	UUID   string `json:"uuid"`
	Type   string `json:"type"`
	Tenant struct {
		Name string `json:"Name"`
		ID   string `json:"id"`
	} `json:"tenant"`
	Policy struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"policy"`
	Context struct {
		MachineName      string `json:"MachineName"`
		ProtectionPlanID string `json:"ProtectionPlanID"`
	} `json:"context"`
	Updated         time.Time `json:"updatedAt"`
	State           string    `json:"state"`
	StartedByUser   string    `json:"startedByUser"`
	CancelRequested bool      `json:"cancelRequested"`
	Kind            int64     `json:"kind"`
	Result          struct {
		Code  string `json:"code"`
		Error struct {
			Reason  string `json:"reason"`
			Context struct {
				Cause  string `json:"cause_str"`
				Effect string `json:"effect_str"`
			} `json:"context"`
		} `json:"error"`
	} `json:"result"`
}

// see https://github.com/golang/go/issues/33974 to convert this to `x/sync/lockedfile.OpenFile` when possible
func writeTask(t Task, cfg cacheConfig) error {
	filename := cfg.taskPath(t)
	if filepath.Base(filename) == ".json" {
		return nil
	}

	// color.Cyan("logging to disk - %s", filename)
	f, err := lockedfile.OpenFile(
		filename,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		0644,
	)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(t)
}

// readTask reads a given Task from disk.
// During operation, the contents of T are overwritten completely.
// see https://github.com/golang/go/issues/33974 to convert this to `x/sync/lockedfile.OpenFile` when possible
func readTask(path string) (Task, error) {
	var t Task
	f, err := lockedfile.OpenFile(path, os.O_RDONLY, 0644)
	if err != nil {
		return Task{}, err
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(&t)
	return t, err
}

func strInSlice(target string, list []string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func filterTaskState(states []string, in <-chan Task, out chan<- Task) {
	defer close(out)
	for t := range in {
		if strInSlice(t.State, states) {
			out <- t
		}
	}
}

type taskPipelineFunc func(Task) error

func writeTaskPipeline(cfg cacheConfig) taskPipelineFunc {
	return func(task Task) error {
		return writeTask(task, cfg)
	}
}

func splitByPolicySet(policy, noPolicy taskPipelineFunc) taskPipelineFunc {
	return func(t Task) error {
		if t.Policy.ID == "" {
			return noPolicy(t)
		}
		return policy(t)
	}
}

func multiTaskPipelineFunc(children ...taskPipelineFunc) taskPipelineFunc {
	return func(t Task) error {
		for _, next := range children {
			err := next(t)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func filterUpdatesOnly(
	cfg cacheConfig,
	next taskPipelineFunc) taskPipelineFunc {
	return func(t Task) error {
		path := cfg.taskPath(t)
		loaded, err := readTask(path)

		if err != nil {
			if os.IsNotExist(err) {
				// if there was an error reading the other cause it's not there
				return next(t)
			}
			// otherwise if there was an error reading it, just disregard
			return err
		}
		if t.Updated.Before(loaded.Updated) {
			// if its after what's on disk, just skip
			return nil
		}
		return next(t)
	}
}

func (a *AcronisAPI) walkTasks(query url.Values, limit int, next taskPipelineFunc) error {
	var limitStr string
	if query == nil {
		query = url.Values{}
	}
	if limit > 0 {
		limitStr = strconv.Itoa(limit)
	} else {
		limitStr = "100"
	}
	query.Set("lod", "full")
	query.Set("limit", limitStr)
	query.Del("after")

	var prev time.Time
	ts := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()

	newTasks, after, err := a.getTasks(ctx, query)
	if err != nil {
		return err
	}

	// FIXME: improve debugging logging?
	afterAbrev := after
	if len(afterAbrev) > 16 {
		afterAbrev = afterAbrev[:16]
	}
	fmt.Fprintln(os.Stderr, color.CyanString(
		"secs [%d] records [%d] after [%s] first ts [%s]\n",
		int(ts.Sub(prev).Seconds()), len(newTasks), afterAbrev,
		newTasks[0].Updated.Format(time.RFC3339)))

	for _, task := range newTasks {
		if err = next(task); err != nil {
			return err
		}
	}

	for after != "" {
		prev = ts
		ts = time.Now()
		fmt.Fprintln(os.Stderr, color.CyanString(
			"secs [%d] records [%d] after [%s] first ts [%s]\n",
			int(ts.Sub(prev).Seconds()), len(newTasks), after[:16],
			newTasks[0].Updated.Format(time.RFC3339)))

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
		defer cancel()
		query = url.Values{}
		query.Set("after", after)
		query.Set("limit", limitStr)
		newTasks, after, err = a.getTasks(ctx, query)
		if err != nil {
			return err
		}
		for _, task := range newTasks {
			if err = next(task); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *AcronisAPI) getTasks(ctx context.Context, query url.Values) ([]Task, string, error) {
	var respData struct {
		Tasks  []Task `json:"items"`
		Paging struct {
			Cursors struct {
				After string `json:"after"`
			} `json:"cursors"`
		} `json:"paging"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	statusCode, err := a.Call(ctx, http.MethodGet, "api/task_manager/v2/tasks",
		nil, query, nil, &respData)
	if err != nil {
		return []Task{}, "", err
	}
	if statusCode != http.StatusOK {
		return []Task{}, "", fmt.Errorf("error status %d : %s",
			statusCode, respData.Error.Message)
	}

	return respData.Tasks, respData.Paging.Cursors.After, nil
}
