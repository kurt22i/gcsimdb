package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
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

var inputfile = "dbinput.txt"
var skipped = ""
var force bool
var upload bool

func main() {
	var d bool
	flag.BoolVar(&d, "d", false, "skip re-download executable?")
	flag.BoolVar(&force, "f", false, "force rerun all")
	flag.BoolVar(&upload, "u", false, "upload to db")
	flag.Parse()

	err := run(d)

	if err != nil {
		fmt.Printf("Error encountered, ending script: %+v\n", err)
	}
	fmt.Printf("\n%v\n", skipped)

	fmt.Print("\nPress 'Enter' to continue...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

func run(skipDownload bool) error {

	if !skipDownload {
		//download nightly cmd line build
		//https://github.com/genshinsim/gcsim/releases/download/nightly/gcsim.exe
		//err := download("./gcsim.exe", "https://github.com/genshinsim/gcsim/releases/latest/download/gcsim.exe")
		//if err != nil {
		//	return errors.Wrap(err, "")
		//}
	}

	//grab latest hash
	hash, err := getVersion()
	if err != nil {
		return errors.Wrap(err, "")
	}
	// hash = strings.Trim(hash, "\n")
	// log.Println(hash)

	//update DB with new and updated teams
	if !force {
		//updateData()
	}

	//allow time to put aside the teams that were updated multiple times
	//fmt.Print("\nPress 'Enter' to continue...")
	//bufio.NewReader(os.Stdin).ReadBytes('\n')

	//loop through db folder; check hash
	data := loadData("./db")

	//process
	err = process(data, hash)

	out, _ := json.Marshal(data)
	//os.Remove(data[i].filepath)
	os.WriteFile("results.json", out, 0755)
	return nil
}

type jsondata struct {
	Config     string `json:"config_file"`
	Characters []struct {
		Name   string `json:"name"`
		Level  int    `json:"level"`
		MaxLvl int    `json:"max_level"`
		Cons   int    `json:"cons"`
		Weapon struct {
			Name   string `json:"name"`
			Refine int    `json:"refine"`
			Level  int    `json:"level"`
			MaxLvl int    `json:"max_level"`
		} `json:"weapon"`
		Stats   []float64    `json:"stats"`
		Talents TalentDetail `json:"talents"`
	} `json:"char_details"`
	DPSraw    FloatResult `json:"dps"`
	NumTarget int         `json:"target_count"`
	CharDPS   []struct {
		DPS1 FloatResult `json:"1"`
		DPS2 FloatResult `json:"2"`
		DPS3 FloatResult `json:"3"`
	} `json:"damage_by_char_by_targets"`
	DPS float64
}

func updateData() error {
	update, err := os.ReadFile(inputfile)
	if err != nil {
		return err
	}

	lines := strings.Split(string(update), "\n")
	for i := range lines {
		if lines[i] == "" {
			continue //skip blank lines
		}
		info := strings.Split(lines[i], "~")
		if len(info) > 3 { //if someone has ~ in their name
			info2 := strings.Split(lines[i], "~")
			info = []string{"", "", ""}
			info[0] = info2[0]
			info[2] = info2[len(info2)-1]
			for j := 1; j < len(info2)-1; j++ {
				info[1] += info2[j]
			}
		}
		info[2] = strings.Replace(info[2], "\r", "", 1) //remove weird \r char
		data := readURL(info[0])
		filename := getName(data)
		filepath := getPath(filename)
		if filepath != "" {
			updateFile(filepath, data, info)
		} else {
			makeFile(filename+".yaml", data, info)
		}
	}
	return nil
}

func updateFile(path string, data jsondata, info []string) {
	file, err := os.ReadFile(path)
	if err != nil {
		//return errors.Wrap(err, "")
	}
	var d pack
	err = yaml.Unmarshal(file, &d)
	if err != nil {
		//return errors.Wrap(err, "")
	}
	if d.Hash == "" { //if there's no hash, we already updated it this run. To ensure every upgrade gets looked at, only one can happen per team per run.
		fmt.Printf("\t%v was already updated, skipping", info[0])
		skipped += fmt.Sprintf("\t%v was already updated, skipping", info[0])
		return
	} else {
		fmt.Printf("\tupdating %v", info[0])
		skipped += fmt.Sprintf("\tupdating %v", info[0])
	}

	d.filepath = path
	d.Hash = "" //remove hash so it reruns
	d.Config = data.Config
	//fmt.Prtitf("%v", info[2])
	if info[2] != "" { //leave the old desc if new one is empty
		d.Description = info[2]
	}
	if !strings.Contains(d.Author, info[1]) {
		if strings.Count(d.Author, "#") == 0 { //if theres no author just replace
			d.Author = info[1]
		} else if strings.Count(d.Author, "#") == 1 {
			d.Author += " and " + info[1]
		} else {
			d.Author = strings.Replace(d.Author, " and ", ", ", 1)
			d.Author += " and " + info[1]
		}
	}

	saveYaml([]pack{d}, false)
}

func makeFile(filename string, data jsondata, info []string) {
	maxdps := 0.0
	maxdpschar := -1
	for i := range data.CharDPS {
		if data.CharDPS[i].DPS1.Mean > maxdps {
			maxdps = data.CharDPS[i].DPS1.Mean
			maxdpschar = i
		}
	}
	//fmt.Printf("%v", data)
	var d pack
	d.filepath = "./db/" + foldernames[charid(data.Characters[maxdpschar].Name)] + "/" + filename
	d.Config = data.Config
	d.Description = info[2]
	d.Author = info[1]

	saveYaml([]pack{d}, false)
}
func getName(data jsondata) string {
	names := []string{"Paimon", "Paimon", "Paimon", "Paimon"}
	for i, c := range data.Characters {
		names[i] = foldernames[charid(c.Name)]
	}
	sort.Strings(names)
	fname := ""
	for i := range names {
		fname += abbr(names[i])
	}
	return fname
}

var chars = []string{"Ayato", "YaeMiko", "Shenhe", "YunJin", "Itto", "Gorou", "Thoma", "Kokomi", "Raiden", "Sara", "Aloy", "Yoimiya", "Sayu", "Ayaka", "Kazuha",
	"Eula", "Yanfei", "Rosaria", "HuTao", "Xiao", "Ganyu", "Albedo", "Zhongli", "Xinyan", "Tartaglia", "Diona", "Klee", "Venti", "Keqing", "Mona",
	"Qiqi", "Diluc", "Jean", "Sucrose", "Chongyun", "Noelle", "Bennett", "Fischl", "Ningguang", "Xingqiu", "Beidou", "Xiangling", "Razor", "Barbara", "Lisa", "Kaeya", "Amber", "Paimon", "TravelerGeo", "TravelerElectro", "Yelan"}

var foldernames = []string{"Ayato", "Yae", "Shenhe", "Yun Jin", "Itto", "Gorou", "Thoma", "Kokomi", "Raiden", "Sara", "Aloy", "Yoimiya", "Sayu", "Ayaka", "Kazuha",
	"Eula", "Yanfei", "Rosaria", "Hu Tao", "Xiao", "Ganyu", "Albedo", "Zhongli", "Xinyan", "Childe", "Diona", "Klee", "Venti", "Keqing", "Mona",
	"Qiqi", "Diluc", "Jean", "Sucrose", "Chongyun", "Noelle", "Bennett", "Fischl", "Ningguang", "Xingqiu", "Beidou", "Xiangling", "Razor", "Barbara", "Lisa", "Kaeya", "Amber", "Paimon", "GMC", "EMC", "Yelan"}

var abbrs = []string{"at", "ya", "sh", "yj", "it", "gr", "tm", "kk", "rd", "sr", "al", "ym", "sy", "ay", "kz",
	"eu", "yf", "rs", "ht", "xa", "gy", "ab", "zl", "xy", "ch", "dn", "kl", "vn", "kq", "mn",
	"qq", "dl", "jn", "sc", "cy", "nl", "bn", "fs", "ng", "xq", "bd", "xl", "rz", "bb", "ls", "ky", "am", "pm", "gc", "em", "yl"}

func charid(c string) int {
	for i := range chars {
		if strings.ToLower(chars[i]) == c {
			return i
		}
	}
	fmt.Printf("No abbr found for %v", c)
	return -1
}

func abbr(c string) string {
	for i := range foldernames {
		if foldernames[i] == c {
			return abbrs[i]
		}
	}
	fmt.Printf("No abbr found for %v", c)
	return ""
}

func getPath(name string) string {
	pth := ""
	filepath.Walk("./db", func(path string, info os.FileInfo, err error) error {

		if strings.Contains(path, ".gz") {
			return nil //skip gz files
		}

		if strings.Contains(path, name) {
			pth = path
		}

		return nil
	})
	fmt.Printf("\n%v", pth)
	skipped += fmt.Sprintf("\n%v", pth)
	return pth
}

type blah struct {
	Data string `json:"data"`
}

func readURL(url string) (data2 jsondata) {
	spaceClient := http.Client{
		Timeout: time.Second * 2, // Timeout after 2 seconds
	}

	//fmt.Printf("%v", url)
	urlreal := "https://viewer.gcsim.workers.dev/" + url[strings.LastIndex(url, "/"):]

	req, err := http.NewRequest(http.MethodGet, urlreal, nil)
	if err != nil {
		log.Fatal(err)
	}

	res, getErr := spaceClient.Do(req)
	if getErr != nil {
		log.Fatal(getErr)
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	body, readErr := ioutil.ReadAll(res.Body)
	if readErr != nil {
		log.Fatal(readErr)
	}

	idk := blah{}
	data := jsondata{}
	err = json.Unmarshal(body, &idk)
	b64z := idk.Data
	z, err4 := base64.StdEncoding.DecodeString(b64z)
	if err4 != nil {
		fmt.Println(err4)
		return
	}
	r, err2 := zlib.NewReader(bytes.NewReader(z))
	if err2 != nil {
		r, err2 = gzip.NewReader(bytes.NewReader(z))
		if err != nil {
			fmt.Println(err2)
			return
		}
	}
	resul, err3 := ioutil.ReadAll(r)
	if err3 != nil {
		fmt.Println(err3)
		return
	}
	err = json.Unmarshal(resul, &data)
	data.DPS = data.DPSraw.Mean

	if err != nil {
		fmt.Println(err)
		return
	}

	//fix the iterations
	data.Config = reIter.ReplaceAllString(data.Config, "iteration=1000")
	data.Config = reWorkers.ReplaceAllString(data.Config, "workers=30")

	return data
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
	res       result
	jd        jsondata
	raw       []byte
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
type DBData struct {
	Author      string  `json:"author"`
	Description string  `json:"description"`
	Hash        string  `json:"hash"`
	Config      string  `json:"config"`
	DPS         float64 `json:"dps"`
	ViewerKey   string  `json:"viewer_key"`
}

var myClient = &http.Client{Timeout: 10 * time.Second}

func getJson(url string, target interface{}) error {
	r, err := myClient.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	return json.NewDecoder(r.Body).Decode(target)
}

func loadData(dir string) []pack {
	var data []pack

	var data2 []DBData
	getJson("https://viewer.gcsim.workers.dev/gcsimdb", &data2)

	for i := range data2 {
		var d pack
		d.Author = data2[i].Author
		d.Description = data2[i].Description
		d.Hash = data2[i].Hash
		d.Config = changeConfig(data2[i].Config)
		data = append(data, d)
	}

	return data
}

func changeConfig(config string) string {
	cfg := config
	//cfg = strings.Replace(cfg, "swap_delay=12", "", -1)
	cfg += "\ntarget lvl=100 resist=.1;\n"
	return cfg
}

var reIter = regexp.MustCompile(`iteration=(\d+)`)
var reWorkers = regexp.MustCompile(`workers=(\d+)`)
var reMode = regexp.MustCompile(`mode=(\w+)`)

func process(data []pack, latest string) error {
	//make a tmp folder if it doesn't exist
	if _, err := os.Stat("./tmp"); !os.IsNotExist(err) {
		fmt.Println("tmp folder already exists, deleting...")
		// path/to/whatever exists
		os.RemoveAll("./tmp/")
	}
	os.Mkdir("./tmp", 0755)

	fmt.Println("Rerunning configs...")

	for i := range data {
		//only rerun if changed or forced
		/*if !force && data[i].Hash != "" {
			fmt.Printf("\tSkipping %v\n", data[i].filepath)
			continue
		}*/
		data[i].changed = true

		//sort.Slice(data[i].Team, func(k, j int) bool { return data[i].Team[k].Name < data[i].Team[j].Name })

		//fix the iterations
		data[i].Config = reIter.ReplaceAllString(data[i].Config, "iteration=1000")
		data[i].Config = reWorkers.ReplaceAllString(data[i].Config, "workers=30")
		//re run sim
		fmt.Printf("\tRerunning %v", data[i].Description)
		outPath := fmt.Sprintf("./tmp/%v", time.Now().Nanosecond())
		err := runSim(data[i].Config, outPath)
		if err != nil {
			fmt.Printf("%v", data[i].filepath)
			return errors.Wrap(err, "")
		}
		//read the json and populate
		data[i].Hash = latest
		jsonData, err := os.ReadFile(outPath + ".json")
		if err != nil {
			//fmt.Printf("%v", data)
			return errors.Wrap(err, "")
		}
		readResultJSON(jsonData, &data[i])

		//find the mode
		match := reMode.FindStringSubmatch(data[i].Config)
		if match != nil {
			data[i].Mode = match[1]
		}

		//write gz
		writeJSONtoGZ(jsonData, outPath)
		json.Unmarshal(jsonData, &data[i].jd)

		data[i].raw = jsonData
		data[i].gzPath = outPath + ".gz"
		fmt.Printf("\t%v\n", data[i].DPS)
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
	p.res = r

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

func saveYaml(data []pack, end bool) error {

	for i := range data {
		//sort.Slice(data[i].Team, func(k, j int) bool { return data[i].Team[k].Name < data[i].Team[j].Name })
		//overwrite yaml
		out, err := yaml.Marshal(data[i])
		if err != nil {
			return errors.Wrap(err, "")
		}
		os.Remove(data[i].filepath)
		//err = os.WriteFile(data[i].filepath2, data[i].Config, 0755)
		if end && force {
			maxdps := 0.0
			maxdpschar := -1
			for j := range data[i].jd.CharDPS {
				if data[i].jd.CharDPS[j].DPS1.Mean > maxdps {
					maxdps = data[i].jd.CharDPS[j].DPS1.Mean
					maxdpschar = j
				}
			}
			path := "./db/" + foldernames[charid(data[i].jd.Characters[maxdpschar].Name)] + "/" + getName(data[i].jd)
			err = os.WriteFile(path+".yaml", out, 0755)
			writeJSONtoGZ(data[i].raw, path+".gz")
		} else {
			err = os.WriteFile(data[i].filepath, out, 0755)
		}
		if err != nil {
			return errors.Wrap(err, "")
		}
	}
	return nil
}
