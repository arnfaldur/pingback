package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	probing "github.com/prometheus-community/pro-bing"
)

func main() {
	address := flag.String("address", "", "IP address or URL to ping")
	delay := flag.Int("delay", 1000, "Delay between pings in milliseconds")
	groupSize := flag.Int("group", 32, "Number of samples to aggregate together")
	aggregates := flag.Int("aggregates", 2, "Number of aggregate streams")
	flag.Parse()

	if *address == "" {
		fmt.Println("Usage: pingback -address=<IP_or_URL> [-delay=<milliseconds>] [-group=<groupSize>] [-aggregates=<number>]")
		os.Exit(1)
	}
	// if len(os.Getenv("DEBUG")) > 0 {
	// f, err := tea.LogToFile("debug.log", "debug")
	// if err != nil {
	// 	fmt.Println("fatal:", err)
	// 	os.Exit(1)
	// }
	// defer f.Close()
	// }

	model := initialModel(*address, time.Duration(*delay)*time.Millisecond, *groupSize, *aggregates)
	p := tea.NewProgram(&model)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

type model struct {
	address            string
	interval           time.Duration
	initialized        bool
	err                error
	counter            int
	latencyData        []float64
	aggregateCounts    []int
	aggregateData      [][][]float64
	renderedAggregates []string
	renderedLegend     string
	gradientUpdate     bool
	windowWidth        int
	minLatency         float64
	maxLatency         float64
}

func initialModel(address string, interval time.Duration, groupSize, aggregates int) model {
	aggregateCounts := make([]int, aggregates)
	aggregateCounts[0] = groupSize
	for i := range aggregateCounts[1:] {
		aggregateCounts[i+1] = aggregateCounts[i] * groupSize
	}
	aggregateData := make([][][]float64, aggregates)
	for i := range aggregateData {
		streamCount := 1 + int(math.Round(math.Log2(float64(aggregateCounts[i]))))
		aggregateData[i] = make([][]float64, streamCount)
	}
	renderedAggregates := make([]string, aggregates)
	return model{
		initialized:        false,
		aggregateCounts:    aggregateCounts,
		aggregateData:      aggregateData,
		renderedAggregates: renderedAggregates,
		renderedLegend:     "",
		address:            address,
		interval:           interval,
		minLatency:         math.MaxFloat64,
		maxLatency:         0.001,
		gradientUpdate:     true,
		// minLatency:  1,
		// maxLatency:  10000,
		windowWidth: 80,
	}
}

func (m *model) Init() tea.Cmd {
	return m.pingCmd()
}

func (m *model) pingCmd() tea.Cmd {
	return func() tea.Msg {
		pinger, err := probing.NewPinger(m.address)
		if err != nil {
			return errMsg{err}
		}
		pinger.Count = 1
		pinger.Timeout = m.interval
		err = pinger.Run()
		if err != nil {
			return errMsg{err}
		}
		stats := pinger.Statistics()
		if len(stats.Rtts) > 0 {
			latency := stats.Rtts[0].Seconds() * 1000
			m.initialized = true
			return latencyMsg{latency}
		}
		return latencyMsg{math.NaN()}
	}
}

type (
	latencyMsg struct{ latency float64 }
	errMsg     struct{ err error }
)

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case latencyMsg:
		m.processLatency(msg.latency)
		return m, tea.Tick(m.interval, func(t time.Time) tea.Msg {
			return m.pingCmd()()
		})
	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
	}
	return m, nil
}

func (m *model) processLatency(latency float64) {
	if !math.IsNaN(latency) {
		if latency < m.minLatency {
			m.minLatency = latency
			m.gradientUpdate = true
		}
		if latency > m.maxLatency {
			m.maxLatency = latency
			m.gradientUpdate = true
		}
	}

	m.latencyData = append(m.latencyData, latency)

	if len(m.latencyData) > m.windowWidth*65536 {
		m.latencyData = m.latencyData[1:]
	}
	m.counter += 1
	for i := range m.aggregateCounts {
		if m.counter%m.aggregateCounts[i] == 0 && len(m.latencyData) > 0 {
			aggregate := aggregate(m.latencyData[len(m.latencyData)-m.aggregateCounts[i]:])
			for j := range m.aggregateData[i] {
				m.aggregateData[i][j] = append(m.aggregateData[i][j], aggregate[j])
			}
		}

	}
}

func (m *model) getDisplayableStreamEnd(stream []float64) []float64 {
	return stream[max(0, len(stream)-m.windowWidth):]
}

func (m *model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}
	if !m.initialized {
		return "Waiting for first reply"
	}

	header := fmt.Sprintf("Pinging %s every %v ms\n",
		m.address, m.interval.Milliseconds())

	renderedStreams := lipgloss.JoinVertical(lipgloss.Left,
		"Raw Data:", m.renderStream(m.getDisplayableStreamEnd(m.latencyData)),
	)

	for i, agg := range m.aggregateData {
		if m.counter%m.aggregateCounts[i] != 0 && !m.gradientUpdate {
			continue
		}

		renderedAggregate := "Aggregated " + fmt.Sprint(m.aggregateCounts[i]) + ":"
		for j, data := range agg {
			if j == len(agg)-1 {
				data = m.getDisplayableStreamEnd(data)
				glyphs := make([]string, len(data))
				anyDrop := false
				for k, drops := range data {
					if drops == 0 {
						glyphs[k] = " "
					} else {
						character := " "
						if drops < 10 {
							character = fmt.Sprint(drops)
						} else {
							character = string(mapToAlphabet((drops - 10) / (float64(m.aggregateCounts[i]) - 10)))
						}
						glyphs[k] = lipgloss.NewStyle().
							Background(lipgloss.Color("#600060")).Render(character)
						anyDrop = true
					}
				}
				if anyDrop {
					renderedStream := lipgloss.JoinHorizontal(lipgloss.Top, glyphs...)
					renderedAggregate = lipgloss.JoinVertical(
						lipgloss.Top, renderedAggregate, renderedStream)
				}
			} else {
				renderedStream := m.renderStream(m.getDisplayableStreamEnd(data))
				renderedAggregate = lipgloss.JoinVertical(
					lipgloss.Top, renderedAggregate, renderedStream)
			}
		}
		m.renderedAggregates[i] = renderedAggregate
	}
	for _, agg := range m.renderedAggregates {
		renderedStreams = lipgloss.JoinVertical(
			lipgloss.Top, renderedStreams, agg)
	}

	if m.gradientUpdate {
		m.renderedLegend = lipgloss.JoinVertical(lipgloss.Top, "Latency Legend (ms):", m.renderLegend())
		m.gradientUpdate = false
	}

	return lipgloss.JoinVertical(lipgloss.Top, header,
		renderedStreams, m.renderedLegend)

}

func mapToAlphabet(value float64) rune {
	if value < 0 {
		value = 0
	} else if value > 1 {
		value = 1
	}
	return rune('a' + int(value*25)) // 25 = number of steps between 'a' and 'z'
}
func (m *model) renderStream(data []float64) string {
	glyphs := make([]string, len(data))
	for i, lat := range data {
		glyphs[i] = m.latencyToGlyph(lat)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, glyphs...)
}

func (m *model) latencyToGlyph(latency float64) string {
	if math.IsNaN(latency) {
		return lipgloss.NewStyle().
			Background(lipgloss.Color("#600060")).Render("X")
	}
	color := m.latencyToColor(latency)
	return lipgloss.NewStyle().Foreground(color).Render("█")
}

// Linear interpolation between two float64 values
func lerp(a, b, t float64) float64 {
	return a + t*(b-a)
}

// Linear interpolation between two lipgloss.Color values
func lerpColor(colorA, colorB lipgloss.Color, t float64) lipgloss.Color {
	r1, g1, b1, _ := colorA.RGBA()
	r2, g2, b2, _ := colorB.RGBA()

	r := int(lerp(float64(r1/256), float64(r2/256), t))
	g := int(lerp(float64(g1/256), float64(g2/256), t))
	b := int(lerp(float64(b1/256), float64(b2/256), t))

	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, b))
}

// Get gradient color based on ratio
func getGradientColor(colors []lipgloss.Color, ratio float64) lipgloss.Color {
	if ratio <= 0 {
		return colors[0]
	}
	if ratio >= 1 {
		return colors[len(colors)-1]
	}
	scaledRatio := ratio * float64(len(colors)-1)
	index := int(scaledRatio)
	t := scaledRatio - float64(index)
	return lerpColor(colors[index], colors[index+1], t)
}

// Updated latencyToColor function
func (m *model) latencyToColor(latency float64) lipgloss.Color {
	if m.minLatency == m.maxLatency {
		return lipgloss.Color("#00FF00") // Default to green
	}

	ratio := math.Log(latency/m.minLatency) / math.Log(m.maxLatency/m.minLatency)

	gradientHexcodes := []string{
		// "#30123b",
		"#466be3",
		"#29bbec",
		"#31f199",
		"#a3fd3d",
		"#edd03a",
		"#fb8022",
		"#d23105",
		"#7a0403",
	}
	gradientColors := make([]lipgloss.Color, len(gradientHexcodes))
	for i, hexcode := range gradientHexcodes {
		gradientColors[i] = lipgloss.Color(hexcode)
	}

	return getGradientColor(gradientColors, ratio)
}

func (m *model) renderLegend() string {
	// Number of gradient steps
	steps := 90 - 1
	// Collect legend entries
	entries := make([]string, steps+1)
	lengths := make([]int, steps+1)
	for i := 0; i <= steps; i++ {
		ratio := float64(i) / float64(steps)
		latency := m.minLatency * math.Exp(ratio*math.Log(m.maxLatency/m.minLatency))
		label := fmt.Sprintf("%.1f", latency)
		if latency >= 100 {
			label = fmt.Sprintf("%.0f", latency)
		}
		entries[i] = m.latencyToGlyph(latency) + " " + label + " "
		lengths[i] = 3 + len(label)
	}

	widestEntry := 0
	for _, length := range lengths {
		if length > widestEntry {
			widestEntry = length
		}
	}

	for i := range entries {
		entries[i] += strings.Repeat(" ", widestEntry-lengths[i])
	}

	// Get terminal width
	cols := m.windowWidth / widestEntry // Approximate column width
	if cols < 1 {
		cols = 1
	}

	// Generate column-major grid
	rows := (len(entries) + cols - 1) / cols
	grid := make([][]string, cols)
	for i := range grid {
		grid[i] = make([]string, rows)
	}

	for i, entry := range entries {
		col := i / rows
		row := i % rows
		grid[col][row] = entry
	}

	// Join rows into a table
	rowsJoined := make([]string, len(grid))
	for i, row := range grid {
		rowsJoined[i] = lipgloss.JoinVertical(lipgloss.Top, row...)
	}

	return lipgloss.JoinHorizontal(lipgloss.Left, rowsJoined...)
}

func aggregate(data []float64) []float64 {
	innerData := make([]float64, len(data))
	copy(innerData, data)
	lost := 0
	sort.Float64s(innerData)
	for _, v := range innerData {
		if math.IsNaN(v) {
			lost++
		}
	}
	innerData = append(innerData[lost:], innerData[:lost]...)
	result := make([]float64, 0)
	samples := math.Log2(float64(len(innerData)))
	for i := 0; i < int(samples); i++ {
		index := (int(math.Round(float64(i) / ((samples - 1) / (float64(len(innerData)) - 1)))))
		result = append(result, innerData[index])
	}
	result = append(result, float64(lost))
	return result
}

// func aggregate(data []float64) []float64 {
// 	min := math.MaxFloat64
// 	max := 0.0
// 	sum := 0.0
// 	count := 0.0
// 	lost := 0.
// 	for _, v := range data {
// 		if !math.IsNaN(v) {
// 			min = math.Min(min, v)
// 			max = math.Max(max, v)
// 			sum += v
// 			count++
// 		} else {
// 			lost++
// 		}
// 	}
// 	result := make([]float64, 0)
// 	result = append(result, lost)
// 	if count == 0 {
// 		result = append(result, math.NaN(), math.NaN(), math.NaN())
// 	} else {
// 		result = append(result, min, sum/count, max)
// 	}
// 	return result
// }
