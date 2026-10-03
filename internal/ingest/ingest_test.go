package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ol1n/memeshorts/internal/db"
)

func TestImgflipFetchAndStore(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get_memes":
			fmt.Fprintf(w, `{"success":true,"data":{"memes":[
				{"id":"181913649","name":"Drake Hotline Bling","url":"%[1]s/img/drake.jpg","width":1200,"height":1200,"box_count":2},
				{"id":"87743020","name":"Two Buttons","url":"%[1]s/img/buttons.jpg","width":600,"height":908,"box_count":3}]}}`, srv.URL)
		case "/img/drake.jpg", "/img/buttons.jpg":
			w.Write([]byte("\xff\xd8fakejpeg"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f := &Imgflip{URL: srv.URL + "/get_memes", Client: srv.Client()}
	memes, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(memes) != 2 || memes[0].ID != "imgflip:181913649" || memes[0].BoxCount != 2 {
		t.Fatalf("neočekávané memy: %+v", memes)
	}
	if memes[0].Score <= memes[1].Score {
		t.Errorf("score má sledovat pořadí popularity: %v <= %v", memes[0].Score, memes[1].Score)
	}

	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "memes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	n, err := Store(context.Background(), d, srv.Client(), filepath.Join(dir, "originals"), memes)
	if err != nil || n != 2 {
		t.Fatalf("Store: n=%d err=%v", n, err)
	}
	m, err := d.GetMeme(context.Background(), "imgflip:181913649")
	if err != nil {
		t.Fatal(err)
	}
	if m.LocalPath == "" || m.Rights != "template-only" || m.Title != "Drake Hotline Bling" {
		t.Errorf("uložený mem: %+v", m)
	}

	// Opakovaný ingest nesmí smazat lokální cestu ani klasifikaci.
	if err := d.SetClassification(context.Background(), m.ID, "before_after", []byte(`{"format":"before_after"}`)); err != nil {
		t.Fatal(err)
	}
	memes[0].ImageURL = ""
	if _, err := Store(context.Background(), d, srv.Client(), filepath.Join(dir, "originals"), memes[:1]); err != nil {
		t.Fatal(err)
	}
	m2, _ := d.GetMeme(context.Background(), m.ID)
	if m2.LocalPath != m.LocalPath || m2.Format != "before_after" || m2.Classification == nil {
		t.Errorf("re-ingest přepsal data: %+v", m2)
	}
}

func TestImgflipError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"error_message":"nope"}`))
	}))
	defer srv.Close()
	if _, err := (&Imgflip{URL: srv.URL, Client: srv.Client()}).Fetch(context.Background()); err == nil {
		t.Fatal("čekal jsem chybu")
	}
}

func TestRedditFetchFiltersNSFWAndNonImages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if u, p, _ := r.BasicAuth(); u != "id" || p != "sec" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case "/r/memes/top":
			calls++
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"data":{"children":[
				{"data":{"id":"a1","title":"ok","url":"https://i.redd.it/a1.jpg","score":100}},
				{"data":{"id":"a2","title":"nsfw","url":"https://i.redd.it/a2.jpg","over_18":true}},
				{"data":{"id":"a3","title":"video","url":"https://v.redd.it/a3"}}]}}`))
		}
	}))
	defer srv.Close()
	r := NewReddit("id", "sec", "test")
	r.AuthURL, r.APIURL, r.Client, r.Interval = srv.URL+"/token", srv.URL, srv.Client(), 0
	memes, err := r.Fetch(context.Background(), []string{"memes"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(memes) != 1 || memes[0].ID != "reddit:a1" || calls != 1 {
		t.Fatalf("memes=%+v calls=%d", memes, calls)
	}
}
