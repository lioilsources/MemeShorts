package comfy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Všechny šablony v workflows/ se musí načíst a jejich _params mířit na existující vstupy.
func TestWorkflowTemplates(t *testing.T) {
	files, _ := filepath.Glob("../../workflows/*.json")
	if len(files) == 0 {
		t.Fatal("žádné šablony")
	}
	for _, f := range files {
		wf, err := LoadWorkflow(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if len(wf.Params()) == 0 {
			t.Errorf("%s: žádné _params", f)
		}
		for id, n := range wf.Nodes {
			for in, v := range n.Inputs {
				link, ok := v.([]any)
				if !ok || len(link) != 2 {
					continue
				}
				if src, ok := link[0].(string); ok {
					if _, exists := wf.Nodes[src]; !exists {
						t.Errorf("%s: %s.%s odkazuje na neexistující node %s", f, id, in, src)
					}
				}
			}
		}
	}
}

func TestSetUnknownParam(t *testing.T) {
	wf, err := ParseWorkflow([]byte(`{"_params":{"seed":[["1","seed"]]},"1":{"class_type":"KSampler","inputs":{"seed":0}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.Set(map[string]any{"sede": 1}); err == nil {
		t.Error("překlep v parametru má být chyba")
	}
	if err := wf.Set(map[string]any{"seed": 7}); err != nil || wf.Nodes["1"].Inputs["seed"] != 7 {
		t.Errorf("Set: %v %v", err, wf.Nodes["1"].Inputs)
	}
	if _, err := ParseWorkflow([]byte(`{"_params":{"x":[["9","seed"]]},"1":{"class_type":"K","inputs":{}}}`)); err == nil {
		t.Error("_params na neexistující node má být chyba")
	}
}

func TestRunDownloadUpload(t *testing.T) {
	polls := 0
	var submitted map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /prompt", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&submitted)
		w.Write([]byte(`{"prompt_id":"p1"}`))
	})
	mux.HandleFunc("GET /history/p1", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls < 2 {
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"p1":{"outputs":{"20":{"images":[{"filename":"a.png","subfolder":"m","type":"output"}],"animated":[false]}},
			"status":{"completed":true,"status_str":"success","messages":[["execution_start",{"timestamp":1000}],["execution_success",{"timestamp":3500}]]}}}`))
	})
	mux.HandleFunc("GET /view", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filename") != "a.png" || r.URL.Query().Get("subfolder") != "m" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte("PNGDATA"))
	})
	mux.HandleFunc("POST /upload/image", func(w http.ResponseWriter, r *http.Request) {
		f, h, err := r.FormFile("image")
		if err != nil {
			w.WriteHeader(400)
			return
		}
		f.Close()
		json.NewEncoder(w).Encode(map[string]string{"name": h.Filename, "subfolder": "", "type": "input"})
	})
	mux.HandleFunc("GET /object_info/CheckpointLoaderSimple", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"CheckpointLoaderSimple":{"input":{"required":{"ckpt_name":[["sd_xl_base_1.0.safetensors","animagine-xl-4.0.safetensors"],{}]}}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL)
	c.Poll = time.Millisecond
	ctx := context.Background()
	wf, _ := ParseWorkflow([]byte(`{"_params":{"seed":[["1","seed"]]},"_doc":"x","1":{"class_type":"KSampler","inputs":{"seed":0}}}`))
	res, err := c.Run(ctx, wf, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Exec != 2500*time.Millisecond {
		t.Errorf("res: %+v", res)
	}
	if p := submitted["prompt"].(map[string]any); p["_params"] != nil || p["_doc"] != nil || p["1"] == nil {
		t.Errorf("do ComfyUI smí jít jen nody: %v", p)
	}
	dst := filepath.Join(t.TempDir(), "a.png")
	if err := c.Download(ctx, res.Files[0], dst); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "PNGDATA" {
		t.Errorf("download: %q", data)
	}
	if name, err := c.Upload(ctx, dst, "ref.png"); err != nil || name != "ref.png" {
		t.Errorf("upload: %q %v", name, err)
	}
	if ck, err := c.Find(ctx, "CheckpointLoaderSimple", "ckpt_name", "Animagine", "hassaku"); err != nil || ck != "animagine-xl-4.0.safetensors" {
		t.Errorf("find: %q %v", ck, err)
	}
}

func TestRunReportsFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"type":"prompt_outputs_failed_validation"},"node_errors":{"1":{"errors":[{"message":"Value not in list"}]}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wf, _ := ParseWorkflow([]byte(`{"1":{"class_type":"K","inputs":{}}}`))
	if _, err := NewClient(srv.URL).Run(context.Background(), wf, time.Second); err == nil {
		t.Fatal("odmítnutý workflow má vrátit chybu")
	}
}
