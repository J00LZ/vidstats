package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)


// Can both be extracted from the VidIQ api calls that the
// browser plugin makes, just look for a url similar to
// the one we used in this program.
var vidIQAuth = ""
var vidIQDeviceId = ""

// the value of PHPSESSID on the generator site.
var genSession = ""

// ChannelTags are the useful tags
// that we get from the YouTube api
type ChannelTags struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// Channel is the data from the VidIQ api
type Channel *struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Stats *[]*Stats `json:"stats"`
}

// Stats contains the data at set times
// from the VidIQ api.
type Stats struct {
	RecordedAt  time.Time `json:"recorded_at"`
	Subscribers int       `json:"subscribers"`
	Views       int       `json:"views"`
	Videos      int       `json:"videos"`
}

// ChannelList is returned by the random
// channel api, and contains raw
// html code that is included
// in the page.
type ChannelList struct {
	Content string `json:"Content"`
}

func min(x, y int) int {
	if x < y {
		return x
	} else {
		return y
	}

}

func main() {
	file, err := os.Open("./stats.json")
	redo := false
	var channels []Channel
	// if the file does not exist, download the new data
	if err != nil {
		channels = downloadStats()
		redo = true
	} else { // else use the existing file
		defer file.Close()
		body, err := io.ReadAll(file)
		err = json.Unmarshal(body, &channels)
		if err != nil {
			log.Panic(err)
		}
	}
	if channels == nil {
		log.Panic("Channels is nil!")
	}

	ctx := context.Background()

	// generate tags if keys.json exists.
	// aka if we can authenticate with the
	// google/youtube api.
	if _, err = os.Open("./keys.json"); err != nil {
		log.Printf("keys not found, not gathering tag list.")
	} else {
		file, err = os.Open("./tags.json")
		var tags []ChannelTags
		// if tags.json does not exist
		// or we got new channels
		if err != nil || redo {
			// authenticate with the youtube api
			svc, err := youtube.NewService(ctx, option.WithCredentialsFile("./keys.json"))
			if err != nil {
				log.Panic(err)
			}
			// and request a channel service
			cs := youtube.NewChannelsService(svc)
			req := cs.List([]string{"snippet", "topicDetails"})
			// quickly print the channel count
			log.Printf("Channel: %d", len(channels))
			tags = make([]ChannelTags, 0, len(channels))
			// for each channel
			for _, v := range channels {
				// request information about it
				res, err := req.Context(ctx).Id(v.ID).Do()
				if err != nil {
					log.Panic(err)
				}
				// if we have a result
				if len(res.Items) < 1 {
					continue
				}
				// take the first one
				zz := res.Items[0]
				// if it has no tags
				if zz.TopicDetails == nil {
					tags = append(tags, ChannelTags{
						ID:   zz.Id,
						Tags: []string{},
						Name: v.Title,
					})
				} else { //if it does
					tags = append(tags, ChannelTags{
						ID:   zz.Id,
						Tags: zz.TopicDetails.TopicCategories,
						Name: v.Title,
					})
				}
			}
			b, err := json.Marshal(tags)
			if err != nil {
				log.Panic(err)
			}
			f, err := os.Create("./tags.json")
			if err != nil {
				log.Panic(err)
			}
			// write the tags.json
			_, err = f.Write(b)
			if err != nil {
				log.Panic(err)
			}
			log.Printf("Json written!")
		} else {
			defer file.Close()
			body, err := io.ReadAll(file)
			err = json.Unmarshal(body, &tags)
			if err != nil {
				log.Panic(err)
			}
		}
	}

	// converting to csv
	var scienceChannels []Channel
	var others []Channel
	var startDate = time.Now()
	var endDate = time.Unix(0, 0)
	// for each channel
	for _, c := range channels {
		found := false
		// if it's id is in the science channels
		for _, s := range scienceTeam {
			if c.ID == s {
				//append it
				scienceChannels = append(scienceChannels, c)
				found = true
			}
		}
		// else it's a non edu channel
		if !found {
			others = append(others, c)
		}
		// find the first start and end dates
		for _, z := range *c.Stats {
			if z.RecordedAt.Before(startDate) {
				startDate = z.RecordedAt
			}
			if z.RecordedAt.After(endDate) {
				endDate = z.RecordedAt
			}
		}
		// and sort the stats
		sort.Slice(*c.Stats, func(i, j int) bool {
			return (*c.Stats)[i].RecordedAt.Before((*c.Stats)[j].RecordedAt)
		})
	}
	log.Printf("Start: %v, end %v", startDate, endDate)
	startYear := startDate.Year()
	startMonth := startDate.Month() + 1
	endYear := endDate.Year()
	endMonth := endDate.Month()

	//create the csv header
	header := []string{"Channel name"}
	for i := startMonth; i <= 12; i++ {
		header = append(header, i.String()+"-"+strconv.Itoa(startYear))
	}
	for i := time.Month(1); i <= endMonth; i++ {
		header = append(header, i.String()+"-"+strconv.Itoa(endYear))
	}
	csvContent := [][]string{header}

	log.Printf("There are %d science channels and %d other channels!", len(scienceChannels), len(others))
	// first print all the info of the educational channels
	for _, v := range scienceChannels {
		l := createListing(v, startYear, endYear, startMonth, endMonth)
		csvContent = append(csvContent, l)
	}
	//and after that the non edu channels
	for _, v := range others {
		l := createListing(v, startYear, endYear, startMonth, endMonth)
		csvContent = append(csvContent, l)
	}
	// export csv
	if err := csvExport(csvContent); err != nil {
		log.Panic(err)
	}

	log.Printf("CSV made!")
}

func csvExport(data [][]string) error {
	// create the destination csv file
	file, err := os.Create("result.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	for _, value := range data {
		if err := writer.Write(value); err != nil {
			// let's return errors if necessary, rather than having a one-size-fits-all error handler
			return err
		}
	}
	return nil
}

// utility function to put the channel data in the proper field
func createListing(channel Channel, startYear, endYear int, startMonth, endMonth time.Month) []string {
	// start with the channel title
	line := []string{channel.Title}
	st := *channel.Stats
	// and then the average data over the month.
	for i := startMonth; i <= 12; i++ {
		found := false
		for _, zz := range st {
			z2 := *zz
			if z2.RecordedAt.Month() == i && z2.RecordedAt.Year() == startYear {
				line = append(line, strconv.Itoa(z2.Views))
				found = true
			}
		}
		if !found {
			line = append(line, "")
		}

	}
	for i := time.Month(1); i <= endMonth; i++ {
		found := false
		for _, zz := range st {
			z2 := *zz
			if z2.RecordedAt.Month() == i && z2.RecordedAt.Year() == endYear {
				line = append(line, strconv.Itoa(z2.Views))
				found = true
			}
		}
		if !found {
			line = append(line, "")
		}
	}
	return line
}

// Found educational channels via random generator.
var scienceTeam = []string{
	"UC4a-Gbdw7vOaccHmFo40b9g", "UCYO_jab_esuFRV4b17AJtAw", "UCBcljXmuXPok9kT_VGA3adg", "UCJDIGW0ywWw9Kh9_vtwqxXA",
	"UCEBb1b_L6zDS3xTUrIALZOw", "UCoHhuummRZaIVX7bD4t2czg", "UC9-y-6csu5WGm29I7JiwpnA", "UC5029sGTV3cQWk9gh90X6-Q",
	"UC2Few2jF7zWvuxtXgoyat8g", "UCcF_QqLWOatOO5vHHlzK_Hw", "UCq0EGvLTyy-LLT1oUSO_0FQ", "UCoxcjq-8xIDTYp3uz647V5A",
	"UCLv7Gzc3VTO6ggFlXY0sOyw", "UC4EY_qnSeAP1xGsh61eOoJA", "UCmdTJKCLBVMQdPC3_kE7t1w", "UCLnGGRG__uGSPLBLzyhg8dQ",
	"UC-EnprmCZ3OXyAoG7vjVNCA", "UCYgL81lc7DOLNhnel1_J6Vg", "UCThyZpUXvT1atGZ0P1-2Vng", "UCIJ7ElhHMlz9lKh8_-dh4rA",
	"UC4XB8AQCiucZ7324-UaYA4A", "UC6KD6HqLbd24LOI26GeHeWw", "UCiEHVhv0SBMpP75JbzJShqw", "UCngehmCV-65FikHYUV1_qXA",
	"UCMWg8e_4hC6p5abek1VGuMw", "UCIuFVDoogw9ujgLbpTCM3sQ", "UCL9No2CVecC_8WazyduwHaw", "UCCabJxhy6wokraEGgFcYD5g",
	"UCC4FftDQK5gj4Ru2MgvraTw", "UCshPTHWDVDFPT3J-V2xBGRA", "UCxjYJHqLxAyMI0jHEbUnNtg", "UC3g-w83Cb5pEAu5UmRrge-A"}

func addChannels(m *map[string]struct{}, channels ...string) {
	for _, c := range channels {
		(*m)[c] = struct{}{}
	}
}

// first gather 110 youtube channels from the
// random channel api, and then
// use the VidIQ api to get the
// view data for the channels.
// If the view data is null, we remove
// the channel from the list.
// (thus we need more than 100 channels)
func downloadStats() []Channel {
	client := &http.Client{}
	cl := make(map[string]struct{})
	addChannels(&cl, scienceTeam...)
	log.Printf("Default channels %d", len(cl))

	// the 110 channels (at least)
	for len(cl) < 110 {
		st, err := GetChannels(client)
		if err != nil {
			log.Printf("Error: %v", err)
			time.Sleep(5 * time.Second)
		} else {
			for i := range st {
				cl[st[i]] = struct{}{}
			}
			log.Printf("Found %d channels", len(st))
		}

	}
	if len(cl) < 1 {
		log.Panic("wat?")
	}
	log.Printf("Found %d channels total!", len(cl))

	var stats []Channel
	keys := make([]string, 0, len(cl))
	for k := range cl {
		keys = append(keys, k)
	}
	// Request 5 channels until we have
	// seen them all.
	for len(keys) > 0 {
		what := min(5, len(keys))
		var x []string
		x, keys = keys[:what], keys[what:]
		statz, err := DoRequest(client,
			"https://api.vidiq.com/youtube/channels/public/stats?from=2020-04-11&to=2021-03-11&ids="+
				strings.Join(x, ","))
		if err != nil {
			log.Panic(err)
		} else {
			stats = append(stats, statz...)
		}
	}
	log.Printf("Found stats for %d channels", len(stats))

	// yes goland, the value is not null
	//goland:noinspection GoNilness
	for _, v := range stats {
		// this for loop is used to create an average of viewers
		// per month, instead of the value per day.
		var n []*Stats
		m := make(map[time.Month][]Stats)
		for _, z := range *v.Stats {
			if z.Views != 0 {
				m[z.RecordedAt.Month()] = append(m[z.RecordedAt.Month()], *z)
			}

		}
		for _, v := range m {
			vl := len(v)
			if vl != 0 {
				at := v[0].RecordedAt
				subsc := 0
				views := 0
				vids := 0
				for _, z := range v {
					subsc += z.Subscribers
					views += z.Views
					vids += z.Videos
				}
				n = append(n, &Stats{
					RecordedAt:  at,
					Subscribers: subsc / vl,
					Views:       views / vl,
					Videos:      vids / vl,
				})

			}
		}
		v.Stats = &n
	}

	b, err := json.Marshal(stats)
	if err != nil {
		log.Panic(err)
	}
	f, err := os.Create("./stats.json")
	if err != nil {
		log.Panic(err)
	}
	_, err = f.Write(b)
	if err != nil {
		log.Panic(err)
	}
	log.Printf("Json written!")
	return stats
}

// Use the random channel api
// to generate a list of channels.
// Sadly, the other two apis
func GetChannels(client *http.Client) ([]string, error) {
	req, err := http.NewRequest("POST",
		"https://www.generatorslist.com/random/websites/random-youtube-channel/ajax",
		bytes.NewBufferString("numResults=100"))
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	// the session id can be found in the cookies when visiting the site.
	req.Header.Add("Cookie", "PHPSESSID="+genSession)
	req.Header.Add("Referer", "https://www.generatorslist.com/random/websites/random-youtube-channel")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	var channels ChannelList
	err = json.Unmarshal(body, &channels)
	if err != nil {
		return nil, err
	}
	c := channels.Content

	re := regexp.MustCompile("https://www.youtube.com/channel/([\\w\\-]+)")

	return regexToChannel(re.FindAllStringSubmatch(c, -1)), nil
}

// we need the first element of the nth element
// of the partial matches
func regexToChannel(s [][]string) []string {
	zz := make([]string, len(s))
	for i, v := range s {
		zz[i] = v[1]
	}
	return zz
}

func DoRequest(client *http.Client, url string) ([]Channel, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	// you will need to set the device id and the authorization
	// yourself if you want to use this api.
	// you can get them from the VidIQ plugin with some effort.
	req.Header.Add("X-Amplitude-Device-ID", vidIQDeviceId)
	req.Header.Add("Authorization", vidIQAuth)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-Vidiq-Client", "ext vff/3.43.2")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	var channels []Channel
	err = json.Unmarshal(body, &channels)
	if err != nil {
		return nil, err
	}
	return channels, nil
}
