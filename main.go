package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

const ERIndex = 7

func main() {
	var d bool
	var force bool
	flag.BoolVar(&d, "d", false, "skip re-download executable?")
	flag.BoolVar(&force, "f", false, "force rerun all")
	flag.Parse()

	err := run(d, force)

	if err != nil {
		fmt.Printf("Error encountered, ending script: %+v\n", err)
	}

	fmt.Print("\nPress 'Enter' to continue...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

func run(skipDownload bool, force bool) error {

	if !skipDownload {
		//download nightly cmd line build
		//https://github.com/genshinsim/gcsim/releases/download/nightly/gcsim.exe
		err := download("./gcsim.exe", "https://github.com/genshinsim/gcsim/releases/latest/download/gcsim.exe")
		if err != nil {
			return errors.Wrap(err, "")
		}
	}

	//grab latest hash
	hash, err := getVersion()
	if err != nil {
		return errors.Wrap(err, "")
	}
	// hash = strings.Trim(hash, "\n")
	// log.Println(hash)

	//loop through db folder; check hash
	data, err := loadData("./db")
	if err != nil {
		return errors.Wrap(err, "")
	}

	//process
	err = process(data, hash, force)
	if err != nil {
		return errors.Wrap(err, "")
	}

	// for i := range data {
	// 	sort.Slice(data[i].Team, func(k, j int) bool { return data[i].Team[k].Name < data[i].Team[j].Name })
	// }

	//store on cloudflare kv
	err = uploadResults(data)
	if err != nil {
		return errors.Wrap(err, "")
	}

	err = uploadIndex(data)
	if err != nil {
		return errors.Wrap(err, "")
	}

	err = saveYaml(data)
	if err != nil {
		return errors.Wrap(err, "")
	}

	return nil
}

func download(path string, url string) error {
	//remove if exists
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		fmt.Printf("%v already exists, deleting...\n", path)
		// path/to/whatever exists
		os.RemoveAll(path)
	}

	fmt.Printf("Downloading: %v\n", url)
	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return errors.Wrap(err, "")
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(path)
	if err != nil {
		return errors.Wrap(err, "")
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return errors.Wrap(err, "")
}

type pack struct {
	Author      string `yaml:"author" json:"author"`
	Config      string `yaml:"config" json:"config"`
	Description string `yaml:"description" json:"description"`
	//the following are machine generated fields
	Hash      string  `yaml:"hash" json:"hash"`
	Team      []char  `yaml:"team" json:"team"`
	DPS       float64 `yaml:"dps" json:"dps"`
	Mode      string  `yaml:"mode" json:"mode"`
	Duration  float64 `yaml:"duration" json:"duration"`
	NumTarget int     `yaml:"target_count" json:"target_count"`
	ViewerKey string  `yaml:"viewer_key" json:"viewer_key"`
	//unexported stuff
	gzPath    string
	filepath  string
	filepath2 string
	changed   bool
}

type char struct {
	Name    string       `yaml:"name" json:"name"`
	Con     int          `yaml:"con" json:"con"`
	Weapon  string       `yaml:"weapon" json:"weapon"`
	Refine  int          `yaml:"refine" json:"refine"`
	ER      float64      `yaml:"er" json:"er"`
	Talents TalentDetail `yaml:"talents" json:"talents"`
}

type result struct {
	Duration FloatResult `json:"sim_duration"`
	DPS      FloatResult `json:"dps"`
	Targets  []struct {
		Level int `json:"level"`
	} `json:"target_details"`
	Characters []struct {
		Name   string `json:"name"`
		Cons   int    `json:"cons"`
		Weapon struct {
			Name   string `json:"name"`
			Refine int    `json:"refine"`
		} `json:"weapon"`
		Stats   []float64    `json:"stats"`
		Talents TalentDetail `json:"talents"`
	} `json:"char_details"`
}

type TalentDetail struct {
	Attack int `json:"attack"`
	Skill  int `json:"skill"`
	Burst  int `json:"burst"`
}

type FloatResult struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
	SD   float64 `json:"sd"`
}

func loadData(dir string) ([]pack, error) {
	var data []pack

	err := filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return errors.Wrap(err, "")
		}
		//do nothing if is directory
		if info.IsDir() {
			return nil
		}
		fmt.Printf("\tReading file: %v at %v\n", info.Name(), path)
		file, err := os.ReadFile(path)
		if err != nil {
			return errors.Wrap(err, "")
		}
		var d pack
		err = yaml.Unmarshal(file, &d)
		if err != nil {
			return errors.Wrap(err, "")
		}
		d.filepath = path

		data = append(data, d)

		return nil
	})

	return data, err
}

var reIter = regexp.MustCompile(`iteration=(\d+)`)
var reWorkers = regexp.MustCompile(`workers=(\d+)`)
var reMode = regexp.MustCompile(`mode=(\w+)`)

func process(data []pack, latest string, force bool) error {
	//make a tmp folder if it doesn't exist
	if _, err := os.Stat("./tmp"); !os.IsNotExist(err) {
		fmt.Println("tmp folder already exists, deleting...")
		// path/to/whatever exists
		os.RemoveAll("./tmp/")
	}
	os.Mkdir("./tmp", 0755)

	fmt.Println("Rerunning configs...")

	for i := range data {
		//compare hash vs current hash; if not the same rerun
		if !force && data[i].Hash == latest {
			fmt.Printf("\tSkipping %v\n", data[i].filepath)
			continue
		}
		data[i].changed = true

		//sort.Slice(data[i].Team, func(k, j int) bool { return data[i].Team[k].Name < data[i].Team[j].Name })

		//fix the iterations
		data[i].Config = reIter.ReplaceAllString(data[i].Config, "iteration=1000")
		data[i].Config = reWorkers.ReplaceAllString(data[i].Config, "workers=30")
		//re run sim
		fmt.Printf("\tRerunning %v\n", data[i].filepath)
		outPath := fmt.Sprintf("./tmp/%v", time.Now().Nanosecond())
		err := runSim(data[i].Config, outPath)
		if err != nil {
			return errors.Wrap(err, "")
		}
		//read the json and populate
		data[i].Hash = latest
		jsonData, err := os.ReadFile(outPath + ".json")
		if err != nil {
			return errors.Wrap(err, "")
		}
		readResultJSON(jsonData, &data[i])

		//find the mode
		match := reMode.FindStringSubmatch(data[i].Config)
		if match != nil {
			data[i].Mode = match[1]
		}

		//overwrite yaml
		out, err := yaml.Marshal(data[i])
		if err != nil {
			return errors.Wrap(err, "")
		}
		os.Remove(data[i].filepath)
		err = os.WriteFile(data[i].filepath, out, 0755)
		if err != nil {
			return errors.Wrap(err, "")
		}

		//write gz
		writeJSONtoGZ(jsonData, outPath)

		data[i].gzPath = outPath + ".gz"
	}

	return nil
}

func readResultJSON(jsonData []byte, p *pack) error {

	var r result
	err := json.Unmarshal(jsonData, &r)
	if err != nil {
		return errors.Wrap(err, "")
	}

	p.DPS = r.DPS.Mean
	p.Duration = r.Duration.Mean
	p.NumTarget = len(r.Targets)

	team := make([]char, 0, len(r.Characters))

	//sort.Slice(r.Characters, func(i, j int) bool { return r.Characters[i].Name < r.Characters[j].Name })
	/*var q result
	var names []string
	for i, v := range r.Characters {
		names[i] = v.Name
	}
	sort.Strings(names)
	for i := 0; i < len(names); i++ {
		for j := 0; j < len(names); j++ {
			if names[i] == r.Characters[j].Name {
				q.Characters[i] = r.Characters[j]
			}
		}
	}*/

	//team info
	for _, v := range r.Characters {
		var c char
		c.Name = v.Name
		c.Con = v.Cons
		c.Weapon = v.Weapon.Name
		c.Refine = v.Weapon.Refine
		c.Talents = v.Talents

		//grab er stats
		c.ER = v.Stats[ERIndex]

		team = append(team, c)
	}

	sort.Slice(team, func(i, j int) bool {
		return team[i].Name < team[j].Name
	})

	p.Team = team

	//sort.Slice(p.Team, func(i, j int) bool { return p.Team[i].Name < p.Team[j].Name })
	return nil
}

func writeJSONtoGZ(jsonData []byte, fpath string) error {
	f, err := os.OpenFile(fpath+".gz", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return errors.Wrap(err, "")
	}
	defer f.Close()
	zw := gzip.NewWriter(f)
	zw.Write(jsonData)
	err = zw.Close()
	return errors.Wrap(err, "")
}

func getVersion() (string, error) {
	fmt.Println("Getting last hash...")
	out, err := exec.Command("./gcsim", "-version").Output()
	hash := strings.Trim(string(out), "\n")
	fmt.Printf("Latest hash: %v\n", hash)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func runSim(cfg, path string) error {
	//write config to file
	err := os.WriteFile(path+".txt", []byte(cfg), 0755)
	if err != nil {
		// fmt.Printf("error saving config file: %v\n", err)
		return errors.Wrap(err, "")
	}
	out, err := exec.Command("./gcsim", "-c", path+".txt", "-out", path+".json").Output()

	if err != nil {
		fmt.Printf("%v\n", string(out))
		return errors.Wrap(err, "")
	}
	return nil
}

type viewerData struct {
	Data        string `json:"data"`
	Author      string `json:"author"`
	Description string `json:"description"`
}

type viewerRes struct {
	ID string `json:"id"`
}

func uploadResults(data []pack) error {
	//read api key from env
	err := godotenv.Load()
	if err != nil {
		return errors.Wrap(err, "error getting env variable")
	}
	apiKey := os.Getenv("API_KEY")
	for i, v := range data {
		//skip if no change and has a viewer key already
		if !v.changed && v.ViewerKey != "" {
			continue
		}
		//check if key exists, if not generate one
		key := v.ViewerKey

		if key == "" {
			key, err = gonanoid.New()
			if err != nil {
				return errors.Wrap(err, "")
			}
		}

		//read the gz file
		gzData, err := os.ReadFile(v.gzPath)
		if err != nil {
			return errors.Wrap(err, "reading gz data")
		}
		b64string := base64.StdEncoding.EncodeToString(gzData)

		x := viewerData{
			Data:        b64string,
			Author:      v.Author,
			Description: "team database",
		}

		jsonData, err := json.Marshal(x)
		if err != nil {
			return errors.Wrap(err, "")
		}

		fmt.Printf("\tUploading results from %v to viewer: ", v.filepath)

		req, err := http.NewRequest("POST", "https://viewer.gcsim.workers.dev/key", bytes.NewBuffer(jsonData))
		if err != nil {
			fmt.Printf("FAILED, error: %v\n", err)
			return errors.Wrap(err, "")
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("API-KEY", apiKey)
		req.Header.Set("VIEWER_KEY", key)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("FAILED, error: %v\n", err)
			return errors.Wrap(err, "")
		}
		if resp.StatusCode != 200 {
			log.Println(resp.Status)
			fmt.Printf("FAILED, error: %v\n", resp.Status)
			return errors.Wrap(errors.New("http post request failed: "+resp.Status), "request failed")
		}

		//otherwise decode key from body
		var res viewerRes
		err = json.NewDecoder(resp.Body).Decode(&res)
		if err != nil {
			fmt.Printf("FAILED, error: %v\n", err)
			return errors.Wrap(err, "")
		}

		data[i].ViewerKey = res.ID
		fmt.Printf("OK, key = %v\n", res.ID)
	}
	return nil
}

func uploadIndex(data []pack) error {
	//read api key from env
	err := godotenv.Load()
	if err != nil {
		return errors.Wrap(err, "")
	}
	apiKey := os.Getenv("API_KEY")

	jsonData, err := json.Marshal(data)
	if err != nil {
		return errors.Wrap(err, "")
	}

	fmt.Print("Uploading DB index: ")

	req, err := http.NewRequest("POST", "https://viewer.gcsim.workers.dev/db", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("FAILED, error: %v\n", err)
		return errors.Wrap(err, "")
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("API-KEY", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("FAILED, error: %v\n", err)
		return errors.Wrap(err, "")
	}
	if resp.StatusCode != 200 {
		log.Println(resp.Status)
		fmt.Printf("FAILED, error: %v\n", resp.Status)
		return errors.Wrap(errors.New("http post request failed: "+resp.Status), "request failed")
	}

	fmt.Print("OK\n")

	return nil
}

func saveYaml(data []pack) error {

	for i := range data {
		//sort.Slice(data[i].Team, func(k, j int) bool { return data[i].Team[k].Name < data[i].Team[j].Name })
		//overwrite yaml
		out, err := yaml.Marshal(data[i])
		if err != nil {
			return errors.Wrap(err, "")
		}
		os.Remove(data[i].filepath)
		err = os.WriteFile(data[i].filepath2, data[i].Config, 0755)
		err = os.WriteFile(data[i].filepath, out, 0755)
		if err != nil {
			return errors.Wrap(err, "")
		}
	}
	return nil
}

// func cloneRepo() (string, error) {
// 	//check if tmp folder already exists, if so remove
// 	if _, err := os.Stat("./tmp"); !os.IsNotExist(err) {
// 		fmt.Println("tmp folder already exists, deleting...")
// 		// path/to/whatever exists
// 		os.RemoveAll("./tmp/")
// 	}

// 	fmt.Println("Starting to clone git repo...")

// 	r, err := git.PlainClone("./tmp", false, &git.Clonehurt every interval=0,1200 amount=0,3000;
  options swap_delay=12{
// 		URL:               "https://github.com/genshinsim/gcsim.git",
// 		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
// 	})
// 	if err != nil {
// 		return "", err
// 	}

// 	ref, err := r.Head()
// 	if err != nil {
// 		return "", err
// 	}

// 	hs := fmt.Sprint(ref.Hash())

// 	fmt.Printf("Git repo cloned successfully, latest hash: %v\n", hs)
// 	return hs, nil
// }
