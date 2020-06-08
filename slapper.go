package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/textproto"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	term "github.com/nsf/termbox-go"
	terminal "github.com/wayneashleyberry/terminal-dimensions"
)

const (
	statsLines             = 3
	movingWindowsSize      = 10 // seconds
	screenRefreshFrequency = 10 // per second
	screenRefreshInterval  = time.Second / screenRefreshFrequency

	reservedWidthSpace  = 40
	reservedHeightSpace = 3

	rateIncreaseStep = 100
	rateDecreaseStep = -100
)

var (
	requestsSent      counter
	responsesReceived counter
	responses         [1024]counter
	desiredRate       counter

	timingsOk  [][]counter
	timingsBad [][]counter

	terminalWidth  uint
	terminalHeight uint

	// plotting vars
	plotWidth  uint
	plotHeight uint

	// first bucket is for requests faster then minY,
	// last of for ones slower then maxY
	buckets    uint
	logBase    float64
	minY, maxY float64
	startMs    float64
)

func resetStats() {
	requestsSent.Store(0)
	responsesReceived.Store(0)

	for _, ok := range timingsOk {
		for i := 0; i < len(ok); i++ {
			ok[i].Store(0)
		}
	}

	for _, bad := range timingsBad {
		for i := 0; i < len(bad); i++ {
			bad[i].Store(0)
		}
	}

	for i := 0; i < len(responses); i++ {
		responses[i].Store(0)
	}
}

type counter int64

type charrange struct {
	min rune
	max rune
}

func (c *counter) Add(v int64) int64 { return atomic.AddInt64((*int64)(c), v) }
func (c *counter) Load() int64       { return atomic.LoadInt64((*int64)(c)) }
func (c *counter) Store(v int64)     { atomic.StoreInt64((*int64)(c), v) }

type targeter struct {
	idx      counter
	requests []request
	header   http.Header
}

type request struct {
	method string
	url    string
	body   []byte
}

func newTargeter(targets string, base64body bool) (*targeter, error) {
	var f *os.File
	var err error

	if targets == "" {
		f = os.Stdin
	} else {
		f, err = os.Open(targets)
		if err != nil {
			return nil, err
		}
		defer f.Close()
	}

	trgt := &targeter{}
	err = trgt.readTargets(f, base64body)

	return trgt, err
}

func (trgt *targeter) readTargets(reader io.Reader, base64body bool) error {
	// syntax
	// GET <url>\n
	// $ <body>\n
	// \n

	var (
		method string
		url    string
		body   []byte
	)

	scanner := bufio.NewScanner(reader)
	var lastLine string
	var line string

	for {
		if lastLine != "" {
			line = lastLine
			lastLine = ""
		} else {
			ok := scanner.Scan()
			if !ok {
				break
			}

			line = strings.TrimSpace(scanner.Text())
		}

		if line == "" {
			continue
		}

		parts := strings.SplitAfterN(line, " ", 2)
		method = strings.TrimSpace(parts[0])
		url = strings.TrimSpace(parts[1])

		ok := scanner.Scan()
		line := strings.TrimSpace(scanner.Text())
		if !ok {
			body = []byte{}
		} else if line == "{}" {
			body = []byte{}
		} else if !strings.HasPrefix(line, "$ ") {
			body = []byte{}
			lastLine = line
		} else {
			line = strings.TrimPrefix(line, "$ ")
			if base64body {
				var err error
				body, err = base64.StdEncoding.DecodeString(line)
				if err != nil {
					return err
				}
			} else {
				body = []byte(line)
			}
		}
		urls, err := parseUrl(url)
		if err != nil {
			return err
		}
		requests := make([]request, len(urls))
		for i, url := range urls {
			requests[i] = request{
				method: method,
				url:    url,
				body:   body,
			}
		}
		trgt.requests = append(trgt.requests, requests...)
	}

	return nil
}

// parseUrl will expand any urls containing random/range syntax
func parseUrl(url string) ([]string, error) {
	detect := regexp.MustCompile(`\[(r?[^\]]*)\]`)
	matches := detect.FindAllStringSubmatch(url, -1)
	orgurl := url
	if res := strings.SplitN(url, " ", 2); len(res) == 2 {
		url = res[0]
	}
	if len(matches) == 0 {
		return []string{url}, nil
	}

	count, err := getCount(orgurl)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, match := range matches {
		if len(match) != 2 {
			return result, errors.New("unexpected matches")
		}
		fullmatch := match[0]
		submatch := match[1]
		if string(submatch[0]) == "r" {
			rargs := strings.Split(match[1][1:], ";")
			if len(rargs) != 2 {
				return nil, fmt.Errorf("need exactly three arguments for random url matches, got %d (%s)", len(rargs), rargs)
			}
			l, err := strconv.Atoi(rargs[0])
			if err != nil {
				return nil, fmt.Errorf("error parsing length: %s", err)
			}
			if count == 0 {
				return nil, errors.New("count was zero or missing")
			}
			a := rargs[1]
			ranges := strings.Split(a, "_")
			var cr []charrange
			for _, r := range ranges {
				minmax := strings.Split(r, "-")
				if len(minmax) != 2 || len(minmax[0]) > 1 || len(minmax[1]) > 1 {
					return nil, errors.New("invalid range")
				}
				cr = append(cr, charrange{min: []rune(minmax[0])[0], max: []rune(minmax[1])[0]})
			}
			randstr := randomString(cr, l, count)
			if result == nil {
				result = make([]string, count)
				for i := range result {
					result[i] = url
				}
			}
			for i := range result {
				result[i] = strings.Replace(result[i], fullmatch, randstr[i], -1)
			}
		} else { // assume it's just a range
			fmt.Println(submatch, fullmatch)
			min, _, err := getMinMax(submatch)
			if err != nil {
				return nil, err
			}
			if result == nil {
				result = make([]string, count)
				for i := range result {
					result[i] = url
				}
			}
			for i := range result {
				result[i] = strings.Replace(result[i], fullmatch, strconv.Itoa(i+min), -1)
			}
		}
	}

	return result, nil
}

// getCount will extract the count from a url, either by parsing the range or getting an explicit count. Range trumps a count
func getCount(url string) (int, error) {
	var count int
	rng := regexp.MustCompile(`\[(\d+-\d+)\]`)
	matches := rng.FindAllStringSubmatch(url, -1)
	fmt.Println(matches)
	if len(matches) > 0 {
		for _, match := range matches {
			sub := match[1]
			if sub == "" {
				continue
			}
			min, max, err := getMinMax(sub)
			if err != nil {
				return 0, err
			}
			if count == 0 {
				count = (max - min) + 1
				continue
			}
			count *= (max - min) + 1
		}
		return count, nil
	}
	// If no range in url, get the explicit range from the passed string
	split := strings.SplitN(url, " ", 2)
	if len(split) == 0 {
		return 0, errors.New("url parse error, no count")
	}
	if len(split) != 2 {
		return 0, errors.New("url parse error, invalid syntax")
	}
	var err error
	count, err = strconv.Atoi(split[1])
	if err != nil {
		return 0, fmt.Errorf("error parsing count: %s", err)
	}
	return count, nil
}

// randomString generates count random strings from the given range specifications
func randomString(charranges []charrange, length, count int) []string {
	var charlist string
	for _, r := range charranges {
		charlist += makeCharList(r)
	}
	result := make([]string, count)
	for i := 0; i < count; i++ {
		b := make([]byte, length)
		for i := range b {
			b[i] = charlist[rand.Int63()%int64(len(charlist))]
		}
		result[i] = string(b)
	}

	return result
}

func getMinMax(stmt string) (int, int, error) {
	minmax := strings.Split(stmt, "-")
	if len(minmax) != 2 {
		return 0, 0, errors.New("range parse error, did not find min or max")
	}
	min, err := strconv.Atoi(minmax[0])
	if err != nil {
		return 0, 0, errors.New("range parse error, min not an integer")
	}
	max, err := strconv.Atoi(minmax[1])
	if err != nil {
		return 0, 0, errors.New("range parse error, max not an integer")
	}
	if min > max {
		return 0, 0, errors.New("invalid range")
	}
	return min, max, nil
}

// makeCharList returns a string with all chars expressed from the range in
func makeCharList(in charrange) string {
	var out string
	if in.min > in.max {
		return ""
	}
	for c := in.min; c <= in.max; c++ {
		out += string(c)
	}
	return out
}

func (trgt *targeter) nextRequest() (*http.Request, error) {
	if len(trgt.requests) == 0 {
		return nil, errors.New("no requests")
	}

	idx := int(trgt.idx.Add(1))
	st := trgt.requests[idx%len(trgt.requests)]

	req, err := http.NewRequest(
		st.method,
		st.url,
		bytes.NewReader(st.body),
	)
	if err != nil {
		return req, err
	}

	for key, headers := range trgt.header {
		for _, header := range headers {
			if key == "Host" {
				req.Host = header
			} else {
				req.Header.Add(key, header)
			}
		}
	}

	return req, err
}

func attack(trgt *targeter, timeout time.Duration, ch <-chan time.Time, quit <-chan struct{}) {
	tr := &http.Transport{
		DisableKeepAlives:   false,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     30 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}

	for {
		select {
		case <-ch:
			if request, err := trgt.nextRequest(); err == nil {
				requestsSent.Add(1)

				start := time.Now()
				response, err := client.Do(request)
				if err == nil {
					_, err = ioutil.ReadAll(response.Body)
					response.Body.Close()
				}
				now := time.Now()

				elapsed := now.Sub(start)
				elapsedMs := float64(elapsed) / float64(time.Millisecond)
				correctedElapsedMs := elapsedMs - startMs
				elapsedBucket := int(math.Log(correctedElapsedMs) / math.Log(logBase))

				// first bucket is for requests faster then minY,
				// last of for ones slower then maxY
				if elapsedBucket < 0 {
					elapsedBucket = 0
				} else if elapsedBucket >= int(buckets)-1 {
					elapsedBucket = int(buckets) - 1
				} else {
					elapsedBucket = elapsedBucket + 1
				}

				responsesReceived.Add(1)

				status := 0
				if err == nil {
					status = response.StatusCode
				}

				responses[status].Add(1)
				tOk, tBad := getTimingsSlot(now)
				if status >= 200 && status < 300 {
					tOk[elapsedBucket].Add(1)
				} else {
					tBad[elapsedBucket].Add(1)
				}
			}
		case <-quit:
			return
		}
	}
}

func reporter(quit <-chan struct{}) {
	fmt.Print("\033[H")
	for i := 0; i < int(terminalHeight); i++ {
		fmt.Println(string(bytes.Repeat([]byte(" "), int(terminalWidth)-1)))
	}

	var currentRate counter
	go func() {
		var lastSent int64
		for range time.Tick(time.Second) {
			curr := requestsSent.Load()
			currentRate.Store(curr - lastSent)
			lastSent = curr
		}
	}()

	colors := []string{
		"\033[38;5;46m", "\033[38;5;47m", "\033[38;5;48m", "\033[38;5;49m", // green
		"\033[38;5;149m", "\033[38;5;148m", "\033[38;5;179m", "\033[38;5;176m", // yellow
		"\033[38;5;169m", "\033[38;5;168m", "\033[38;5;197m", "\033[38;5;196m", // red
	}

	colorMultiplier := float64(len(colors)) / float64(buckets)
	barWidth := int(plotWidth) - reservedWidthSpace // reserve some space on right and left

	ticker := time.Tick(screenRefreshInterval)
	for {
		select {
		case <-ticker:
			// scratch arrays
			tOk := make([]int64, len(timingsOk))
			tBad := make([]int64, len(timingsBad))

			// need to understand how long in longest bar,
			// also take a change to copy arrays to have consistent view

			max := int64(1)
			for i := 0; i < len(timingsOk); i++ {
				ok := timingsOk[i]
				bad := timingsBad[i]

				for j := 0; j < len(ok); j++ {
					tOk[j] += ok[j].Load()
					tBad[j] += bad[j].Load()
					if sum := tOk[j] + tBad[j]; sum > max {
						max = sum
					}
				}
			}

			sent := requestsSent.Load()
			recv := responsesReceived.Load()
			fmt.Print("\033[H") // clean screen
			fmt.Printf("sent: %-6d ", sent)
			fmt.Printf("in-flight: %-2d ", sent-recv)
			fmt.Printf("\033[96mrate: %4d/%d RPS\033[0m ", currentRate.Load(), desiredRate.Load())

			fmt.Print("responses: ")
			for status, counter := range responses {
				if c := counter.Load(); c > 0 {
					if status >= 200 && status < 300 {
						fmt.Printf("\033[32m[%d]: %-6d\033[0m ", status, c)
					} else {
						fmt.Printf("\033[31m[%d]: %-6d\033[0m ", status, c)
					}
				}
			}
			fmt.Print("\r\n\r\n")

			width := float64(barWidth) / float64(max)
			for bkt := uint(0); bkt < buckets; bkt++ {
				var label string
				if bkt == 0 {
					if startMs >= 10 {
						label = fmt.Sprintf("<%.0f", startMs)
					} else {
						label = fmt.Sprintf("<%.1f", startMs)
					}
				} else if bkt == buckets-1 {
					if maxY >= 10 {
						label = fmt.Sprintf("%3.0f+", maxY)
					} else {
						label = fmt.Sprintf("%.1f+", maxY)
					}
				} else {
					beginMs := minY + math.Pow(logBase, float64(bkt-1))
					endMs := minY + math.Pow(logBase, float64(bkt))

					if endMs >= 10 {
						label = fmt.Sprintf("%3.0f-%3.0f", beginMs, endMs)
					} else {
						label = fmt.Sprintf("%.1f-%.1f", beginMs, endMs)
					}
				}

				widthOk := int(float64(tOk[bkt]) * width)
				widthBad := int(float64(tBad[bkt]) * width)
				widthLeft := barWidth - widthOk - widthBad

				fmt.Printf("%10s ms: [%s%6d%s/%s%6d%s] %s%s%s%s%s \r\n",
					label,
					"\033[32m",
					tOk[bkt],
					"\033[0m",
					"\033[31m",
					tBad[bkt],
					"\033[0m",
					colors[int(float64(bkt)*colorMultiplier)],
					bytes.Repeat([]byte("E"), widthBad),
					bytes.Repeat([]byte("*"), widthOk),
					bytes.Repeat([]byte(" "), widthLeft),
					"\033[0m")
			}
		case <-quit:
			return
		}
	}
}

func keyPressListener(rateChanger chan<- int64) {
	// start keyPress listener
	err := term.Init()
	if err != nil {
		log.Fatal(err)
	}

	defer term.Close()

keyPressListenerLoop:
	for {
		switch ev := term.PollEvent(); ev.Type {
		case term.EventKey:
			switch ev.Key {
			case term.KeyCtrlC:
				break keyPressListenerLoop
			default:
				switch ev.Ch {
				case 'q':
					break keyPressListenerLoop
				case 'r':
					resetStats()
				case 'k': // up
					rateChanger <- rateIncreaseStep
				case 'j':
					rateChanger <- rateDecreaseStep
				}
			}
		case term.EventError:
			log.Fatal(ev.Err)
		}
	}
}

func ticker(rate uint64, quit <-chan struct{}) (<-chan time.Time, chan<- int64) {
	ticker := make(chan time.Time, 1)
	rateChanger := make(chan int64, 1)

	// start main workers
	go func() {
		desiredRate.Store(int64(rate))
		tck := time.NewTicker(time.Duration(1e9 / rate))

		for {
			select {
			case r := <-rateChanger:
				tck.Stop()
				if newRate := desiredRate.Add(r); newRate > 0 {
					tck = time.NewTicker(time.Duration(1e9 / newRate))
				} else {
					desiredRate.Store(0)
				}
			case t := <-tck.C:
				ticker <- t
			case <-quit:
				return
			}
		}
	}()

	return ticker, rateChanger
}

func getTimingsSlot(now time.Time) ([]counter, []counter) {
	n := int(now.UnixNano() / 100000000)
	slot := n % len(timingsOk)
	return timingsOk[slot], timingsBad[slot]
}

func initializeTimingsBucket(buckets uint) {
	timingsOk = make([][]counter, movingWindowsSize*screenRefreshFrequency)
	for i := 0; i < len(timingsOk); i++ {
		timingsOk[i] = make([]counter, buckets)
	}

	timingsBad = make([][]counter, movingWindowsSize*screenRefreshFrequency)
	for i := 0; i < len(timingsBad); i++ {
		timingsBad[i] = make([]counter, buckets)
	}

	go func() {
		for now := range time.Tick(screenRefreshInterval) {
			// TODO account for missing ticks
			// clean next timing slot which is last one in ring buffer
			next := now.Add(screenRefreshInterval)
			tOk, tBad := getTimingsSlot(next)
			for i := 0; i < len(tOk); i++ {
				tOk[i].Store(0)
			}

			for i := 0; i < len(tBad); i++ {
				tBad[i].Store(0)
			}
		}
	}()
}

type arrayFlags []string

func (i *arrayFlags) String() string {
	return fmt.Sprintf("+%v", *i)
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

var headerFlags arrayFlags

func main() {
	workers := flag.Uint("workers", 8, "Number of workers")
	timeout := flag.Duration("timeout", 30*time.Second, "Requests timeout")
	targets := flag.String("targets", "", "Targets file")
	base64body := flag.Bool("base64body", false, "Bodies in targets file are base64-encoded")
	rate := flag.Uint64("rate", 50, "Requests per second")
	miY := flag.Duration("minY", 0, "min on Y axe (default 0ms)")
	maY := flag.Duration("maxY", 100*time.Millisecond, "max on Y axe")
	flag.Var(&headerFlags, "H", "HTTP header 'key: value' set on all requests. Repeat for more than one header.")
	flag.Parse()

	terminalWidth, _ = terminal.Width()
	terminalHeight, _ = terminal.Height()

	plotWidth = terminalWidth
	plotHeight = terminalHeight - statsLines

	if plotWidth <= reservedWidthSpace {
		log.Fatal("not enough screen width, min 40 characters required")
	}

	if plotHeight <= reservedHeightSpace {
		log.Fatal("not enough screen height, min 3 lines required")
	}

	minY, maxY = float64(*miY/time.Millisecond), float64(*maY/time.Millisecond)
	deltaY := maxY - minY
	buckets = plotHeight
	logBase = math.Pow(deltaY, 1/float64(buckets-2))
	startMs = minY + math.Pow(logBase, 0)

	initializeTimingsBucket(buckets)

	quit := make(chan struct{}, 1)
	ticker, rateChanger := ticker(*rate, quit)

	trgt, err := newTargeter(*targets, *base64body)
	if err != nil {
		log.Fatal(err)
	}

	if len(headerFlags) > 0 {
		headers := strings.Join(headerFlags, "\r\n")
		headers += "\r\n\r\n"                                                  // Need an extra \r\n at the end
		tp := textproto.NewReader(bufio.NewReader(strings.NewReader(headers))) // Never change, Go

		mimeHeader, err := tp.ReadMIMEHeader()
		if err != nil {
			log.Fatal(err)
		}

		trgt.header = http.Header(mimeHeader)
	}

	// start attackers
	var wg sync.WaitGroup
	for i := uint(0); i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attack(trgt, *timeout, ticker, quit)
		}()
	}

	// start reporter
	wg.Add(1)
	go func() {
		defer wg.Done()
		reporter(quit)
	}()

	keyPressListener(rateChanger)

	// bye
	close(quit)
	wg.Wait()
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
