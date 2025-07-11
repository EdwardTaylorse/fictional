package tff

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/holoplot/go-evdev"
	"github.com/stretchr/testify/require"
)

type writeToSlice struct {
	s []Event
}

func (wts *writeToSlice) WriteOne(ev *evdev.InputEvent) error {
	wts.s = append(wts.s, *ev)
	return nil
}

func (wts *writeToSlice) requireEqual(t *testing.T, expectedShort string) {
	t.Helper()
	actualShort, err := csvToShortCsv(eventsToCsv(wts.s))
	if err != nil {
		t.Fatal(err.Error())
	}
	var e []string
	for _, line := range strings.Split(expectedShort, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		e = append(e, line)

	}
	expectedShort = strings.Join(e, "\n")
	require.Equal(t, expectedShort, actualShort)
}

func csvToShortCsv(csv string) (string, error) {
	var e []string
	for _, line := range strings.Split(csv, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line, err := csvLineToShortLine(line)
		if err != nil {
			return "", err
		}
		e = append(e, line)
	}
	return strings.Join(e, "\n"), nil
}

func AssertComboCSVInputOutput(t *testing.T, input string, expectedOutput string, allCombos []*Combo) {
	_assertInputOutput(t, input, expectedOutput, allCombos, NewReadFromSliceInputCSV)
}

func AssertComboStateStringInputOutput(t *testing.T, input string, expectedOutput string, allCombos []*Combo) {
	_assertInputOutput(t, input, expectedOutput, allCombos, NewReadFromSliceInputStateString)
}

func _assertInputOutput(t *testing.T, input string, expectedOutput string, allCombos []*Combo,
	stringToEventsFunc func(string) (*readFromSlice, error),
) {
	t.Helper()
	ew := writeToSlice{}
	er, err := stringToEventsFunc(input)
	require.Nil(t, err)
	err = manInTheMiddle(er, &ew, allCombos, true, true)
	require.ErrorIs(t, err, io.EOF)
	ew.requireEqual(t, expectedOutput)
}

var csvLineToShortLineRegex = regexp.MustCompile(`^\d+;\d+;EV_KEY;KEY_(\w+);(\w+)$`)

func csvLineToShortLine(csvLine string) (string, error) {
	matches := csvLineToShortLineRegex.FindStringSubmatch(csvLine)
	if matches == nil || len(matches) != 3 {
		return "", fmt.Errorf("failed to parse csvLine %s %+v", csvLine, matches)
	}
	return fmt.Sprintf("%s-%s", matches[1], matches[2]), nil
}

var _ = EventWriter(&writeToSlice{})

type readFromSlice struct {
	s []evdev.InputEvent
}

func (rfs *readFromSlice) ReadOne() (*Event, error) {
	if len(rfs.s) == 0 {
		return nil, io.EOF
	}
	ev := rfs.s[0]
	rfs.s = rfs.s[1:]
	return &ev, nil
}

func (rfs *readFromSlice) loadCSV(csvString string) error {
	s, err := csvToSlice(csvString)
	rfs.s = s
	return err
}

func stateStringToSlice(stateString string) ([]evdev.InputEvent, error) {
	var timeVal syscall.Timeval
	syscall.Gettimeofday(&timeVal)
	timeVal.Usec = 0
	parts := strings.Fields(stateString)
	if len(parts)%2 != 1 {
		return nil, fmt.Errorf("stateString %q has an evem number of parts. "+
			"Need something like 'f_ (259.006ms) j_ (105.844ms) j/ (721.7ms) f/", stateString)
	}
	events := make([]evdev.InputEvent, 0, len(parts))
	for i, part := range parts {
		if i%2 == 0 {
			// is key
			lastChar := part[len(part)-1]
			var value int32
			switch lastChar {
			case '_':
				value = DOWN
			case '/':
				value = UP
			}
			code, err := wordToKeyCode(part[:len(part)-1])
			if err != nil {
				return nil, err
			}
			events = append(events, evdev.InputEvent{
				Time:  timeVal,
				Type:  evdev.EV_KEY,
				Code:  code,
				Value: value,
			})
			continue
		}
		// is time
		d, err := time.ParseDuration(part[1 : len(part)-1])
		if err != nil {
			return nil, err
		}
		t := syscallTimevalToTime(timeVal)
		t = t.Add(d)
		timeVal = timeToSyscallTimeval(t)
	}
	return events, nil
}

func (rfs *readFromSlice) loadStateString(stateString string) error {
	s, err := stateStringToSlice(stateString)
	rfs.s = s
	return err
}

func NewReadFromSliceInputCSV(csvString string) (*readFromSlice, error) {
	rfs := readFromSlice{}
	err := rfs.loadCSV(csvString)
	return &rfs, err
}

// stateSTring is a string like this:
// capslock_ (259.006ms) u_ (105.844ms) u/ (721.7ms) capslock/
func NewReadFromSliceInputStateString(stateString string) (*readFromSlice, error) {
	rfs := readFromSlice{}
	err := rfs.loadStateString(stateString)
	return &rfs, err
}

var _ EventReader = &readFromSlice{}

var asdfTestEvents = `1712500001;862966;EV_KEY;KEY_A;down
1712500002;22233;EV_KEY;KEY_A;up
1712500002;478346;EV_KEY;KEY_S;down
1712500002;637660;EV_KEY;KEY_S;up
1712500003;35798;EV_KEY;KEY_D;down
1712500003;132219;EV_KEY;KEY_D;up
1712500003;948232;EV_KEY;KEY_F;down
1712500004;116984;EV_KEY;KEY_F;up
`

var fjkCombos = []*Combo{
	{
		Keys:    []KeyCode{evdev.KEY_F, evdev.KEY_J},
		OutKeys: []KeyCode{evdev.KEY_X},
	},
	{
		Keys:    []KeyCode{evdev.KEY_F, evdev.KEY_K},
		OutKeys: []KeyCode{evdev.KEY_Y},
	},
}

func Test_manInTheMiddle_noMatch(t *testing.T) {
	f := func(allCombos []*Combo) {
		ew := writeToSlice{}
		er, err := NewReadFromSliceInputCSV(asdfTestEvents)
		require.Nil(t, err)
		err = manInTheMiddle(er, &ew, allCombos, false, true)
		require.ErrorIs(t, err, io.EOF)
		csv := eventsToCsv(ew.s)
		require.Equal(t, asdfTestEvents, csv)
	}
	f([]*Combo{
		{
			Keys:    []KeyCode{evdev.KEY_A, evdev.KEY_F},
			OutKeys: []KeyCode{evdev.KEY_X},
		},
	})

	f([]*Combo{
		{
			Keys:    []KeyCode{evdev.KEY_G, evdev.KEY_H},
			OutKeys: []KeyCode{evdev.KEY_X},
		},
	})

	f([]*Combo{
		{
			Keys:    []KeyCode{evdev.KEY_G, evdev.KEY_H},
			OutKeys: []KeyCode{evdev.KEY_X},
		},
		{
			Keys:    []KeyCode{evdev.KEY_A, evdev.KEY_K},
			OutKeys: []KeyCode{evdev.KEY_X},
		},
	})
}

// //////////////////////////////////////////////
func Test_manInTheMiddle_NoMatch_JustKeys(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1712500000;000000;EV_KEY;KEY_B;down
	1712500000;020000;EV_KEY;KEY_B;up
	1712500000;700000;EV_KEY;KEY_F;down
	1712500000;720000;EV_KEY;KEY_F;up
	1712500001;100000;EV_KEY;KEY_J;down
	1712500001;110000;EV_KEY;KEY_J;up
	1712500001;800000;EV_KEY;KEY_C;down
	1712500001;900000;EV_KEY;KEY_C;up
	`,
		`
	B-down
	B-up
	F-down
	F-up
	J-down
	J-up
	C-down
	C-up
	`, fjkCombos)
}

func Test_manInTheMiddle_TwoCombos_WithOneEmbrachingMatch(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1712500000;000000;EV_KEY;KEY_B;down
	1712500000;020000;EV_KEY;KEY_B;up
	1712500000;700000;EV_KEY;KEY_F;down
	1712500000;720000;EV_KEY;KEY_J;down
	1712500001;100000;EV_KEY;KEY_J;up
	1712500001;110000;EV_KEY;KEY_F;up
	1712500001;800000;EV_KEY;KEY_C;down
	1712500001;900000;EV_KEY;KEY_C;up
	`,
		`
	B-down
	B-up
	X-down
	X-up
	C-down
	C-up
	`, fjkCombos)
}

func Test_manInTheMiddle_SingleCombo_OneEmbrachingMatch(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1712500003;827714;EV_KEY;KEY_F;down
	1712500003;849844;EV_KEY;KEY_J;down
	1712500004;320867;EV_KEY;KEY_J;up
	1712500004;321153;EV_KEY;KEY_F;up
	`,
		`
	X-down
	X-up
	`,
		fjkCombos)
}

func Test_manInTheMiddle_ComboWithMatch_CrossRhyme(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1712500000;700000;EV_KEY;KEY_F;down
	1712500000;720000;EV_KEY;KEY_J;down
	1712500001;100000;EV_KEY;KEY_F;up
	1712500001;110000;EV_KEY;KEY_J;up
	1712500001;800000;EV_KEY;KEY_C;down
	1712500001;900000;EV_KEY;KEY_C;up
	`,
		`
	X-down
	X-up
	C-down
	C-up
	`, fjkCombos)
}

func Test_manInTheMiddle_ComboWithMatch_SingleUpDown(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1716752333;203961;EV_KEY;KEY_F;down
	1716752333;327486;EV_KEY;KEY_F;up
	`,
		`
	F-down
	F-up
	`,
		fjkCombos)
}

func Test_manInTheMiddle_ComboWithMatch_OverlapNoCombo(t *testing.T) {
	// short overlap between K-down and F-up.
	// This is F followed by K, not a combo.
	AssertComboCSVInputOutput(t, `
	1712500003;827714;EV_KEY;KEY_F;down
	1712500004;320840;EV_KEY;KEY_J;down
	1712500004;320860;EV_KEY;KEY_F;up
	1712500004;321153;EV_KEY;KEY_J;up
	`,
		`
	F-down
	J-down
	F-up
	J-up
	`, fjkCombos)
}

func Test_manInTheMiddle_WithoutMatch(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1712500000;700000;EV_KEY;KEY_K;down
	1712500000;820000;EV_KEY;KEY_K;up
	1712500000;830000;EV_KEY;KEY_F;down
	1712500000;840000;EV_KEY;KEY_F;up
	`,
		`
	K-down
	K-up
	F-down
	F-up
	`, fjkCombos)
}

func Test_manInTheMiddle_TwoComboWithSingleMatch(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1712500000;000000;EV_KEY;KEY_B;down
	1712500000;020000;EV_KEY;KEY_B;up
	1712500000;700000;EV_KEY;KEY_F;down
	1712500000;720000;EV_KEY;KEY_J;down
	1712500001;100000;EV_KEY;KEY_J;up
	1712500001;110000;EV_KEY;KEY_F;up
	1712500001;800000;EV_KEY;KEY_C;down
	1712500001;900000;EV_KEY;KEY_C;up
	`,
		`
	B-down
	B-up
	X-down
	X-up
	C-down
	C-up
	`, fjkCombos)
}

func Test_manInTheMiddle_TwoEmbrachingCombosWithMatch(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1716752333;000000;EV_KEY;KEY_F;down
	1716752333;100000;EV_KEY;KEY_J;down
	1716752333;400000;EV_KEY;KEY_J;up
	1716752333;600000;EV_KEY;KEY_K;down
	1716752333;800000;EV_KEY;KEY_K;up
	1716752334;000000;EV_KEY;KEY_F;up
	`,
		`
	X-down
	X-up
	Y-down
	Y-up
	`,
		fjkCombos)
}

func Test_manInTheMiddle_TwoJoinedCombos_FirstKeyDownUntilEnd(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1716752333;000000;EV_KEY;KEY_F;down
	1716752333;100000;EV_KEY;KEY_J;down
	1716752333;400000;EV_KEY;KEY_J;up
	1716752333;600000;EV_KEY;KEY_K;down
	1716752333;800000;EV_KEY;KEY_K;up
	1716752334;000000;EV_KEY;KEY_F;up
	`,
		`
	X-down
	X-up
	Y-down
	Y-up
	`,
		fjkCombos)
}

func Test_manInTheMiddle_Unrelated_Embraced_Keystrokes(t *testing.T) {
	AssertComboCSVInputOutput(t, `
	1716752333;000000;EV_KEY;KEY_F;down
	1716752333;100000;EV_KEY;KEY_W;down
	1716752333;400000;EV_KEY;KEY_W;up
	1716752334;000000;EV_KEY;KEY_F;up
	1716752334;100000;EV_KEY;KEY_RFKILL;up
	`,
		`
	F-down
	W-down
	W-up
	F-up
	`,
		fjkCombos)
}

func Test_manInTheMiddle_ComboWithMatch_NoPanic(t *testing.T) {
	// This test is to ensure that no panic happens.
	// Output could be different.
	AssertComboCSVInputOutput(t, `
	1712500000;000000;EV_KEY;KEY_F;down
	1712500000;064000;EV_KEY;KEY_K;down
	1712500000;128000;EV_KEY;KEY_F;up
	1712500000;144000;EV_KEY;KEY_J;down
	1712500000;208000;EV_KEY;KEY_K;up
	1712500000;224000;EV_KEY;KEY_F;down
`,
		// The input is quite crazy. This tests ensures that no panic happens.
		// Changes are allowed to alter the output.
		`
	Y-down
    Y-up
	K-down
	J-down
	K-up
	F-down
	`, fjkCombos)
}

//////////////////////////////

var orderedCombos = []*Combo{
	{
		Keys:    []KeyCode{evdev.KEY_F, evdev.KEY_J},
		OutKeys: []KeyCode{evdev.KEY_X},
	},
	{
		Keys:    []KeyCode{evdev.KEY_J, evdev.KEY_F},
		OutKeys: []KeyCode{evdev.KEY_A},
	},
	{
		Keys:    []KeyCode{evdev.KEY_F, evdev.KEY_K},
		OutKeys: []KeyCode{evdev.KEY_Y},
	},
	{
		Keys:    []KeyCode{evdev.KEY_J, evdev.KEY_K},
		OutKeys: []KeyCode{evdev.KEY_B},
	},
}

var capslockCombos = []*Combo{
	{
		Keys:    []KeyCode{evdev.KEY_CAPSLOCK, evdev.KEY_J},
		OutKeys: []KeyCode{evdev.KEY_BACKSPACE},
	},
}

func Test_orderedCombos(t *testing.T) {
	AssertComboCSVInputOutput(t,
		`
	1712500000;000000;EV_KEY;KEY_F;down
	1712500000;060000;EV_KEY;KEY_J;down
	1712500000;120000;EV_KEY;KEY_F;up
	1712500000;200000;EV_KEY;KEY_J;up

	1712500001;000000;EV_KEY;KEY_J;down
	1712500001;060000;EV_KEY;KEY_F;down
	1712500001;120000;EV_KEY;KEY_J;up
	1712500001;200000;EV_KEY;KEY_F;up
	`,
		`
		X-down
		X-up
		A-down
		A-up
	`,
		orderedCombos)
}

func Test_Capslock_Navigation(t *testing.T) {
	AssertComboStateStringInputOutput(t,
		`
		capslock_ (259.006ms) j_ (105.844ms) j/ (721.7ms) capslock/
	`,
		`
		BACKSPACE-down
		BACKSPACE-up
	`,
		capslockCombos)
}

func Test_ShouldNotPanic(t *testing.T) {
	log := `|>>1737965475;912716;EV_MSC;MSC_SCAN;458769
|>>1737965475;912716;EV_KEY;KEY_N;down
|>>1737965475;912716;EV_SYN;SYN_REPORT;up
|>>1737965476;163526;EV_KEY;KEY_N;repeat
|>>1737965476;163526;EV_SYN;SYN_REPORT;down
|>>1737965476;197504;EV_KEY;KEY_N;repeat
|>>1737965476;197504;EV_SYN;SYN_REPORT;down
|>>1737965476;231448;EV_KEY;KEY_N;repeat
|>>1737965476;231448;EV_SYN;SYN_REPORT;down
|>>1737965476;265450;EV_KEY;KEY_N;repeat
|>>1737965476;265450;EV_SYN;SYN_REPORT;down
|>>1737965476;300444;EV_KEY;KEY_N;repeat
|>>1737965476;300444;EV_SYN;SYN_REPORT;down
|>>1737965476;335450;EV_KEY;KEY_N;repeat
|>>1737965476;335450;EV_SYN;SYN_REPORT;down
|>>1737965476;369448;EV_KEY;KEY_N;repeat
|>>1737965476;369448;EV_SYN;SYN_REPORT;down
|>>1737965476;403445;EV_KEY;KEY_N;repeat
|>>1737965476;403445;EV_SYN;SYN_REPORT;down
|>>1737965476;437445;EV_KEY;KEY_N;repeat
|>>1737965476;437445;EV_SYN;SYN_REPORT;down
|>>1737965476;471452;EV_KEY;KEY_N;repeat
|>>1737965476;471452;EV_SYN;SYN_REPORT;down
|>>1737965476;506444;EV_KEY;KEY_N;repeat
|>>1737965476;506444;EV_SYN;SYN_REPORT;down
|>>1737965476;540446;EV_KEY;KEY_N;repeat
|>>1737965476;540446;EV_SYN;SYN_REPORT;down
|>>1737965476;574452;EV_KEY;KEY_N;repeat
|>>1737965476;574452;EV_SYN;SYN_REPORT;down
|>>1737965476;600611;EV_MSC;MSC_SCAN;458809
|>>1737965476;600611;EV_KEY;KEY_CAPSLOCK;down
|>>1737965476;600611;EV_SYN;SYN_REPORT;up
|>>1737965476;792606;EV_MSC;MSC_SCAN;458769
|>>1737965476;792606;EV_KEY;KEY_N;up
|>>1737965476;792606;EV_SYN;SYN_REPORT;up
|>>1737965477;104606;EV_MSC;MSC_SCAN;458809
|>>1737965477;104606;EV_KEY;KEY_CAPSLOCK;up
|>>1737965477;104606;EV_SYN;SYN_REPORT;up
|>>1737965477;488608;EV_MSC;MSC_SCAN;458769
|>>1737965477;488608;EV_KEY;KEY_N;down`
	scanner := bufio.NewScanner(strings.NewReader(string(log)))
	logReader := ComboLogEventReader{scanner: scanner}
	combos, err := LoadYamlFromBytes([]byte(`
combos:
  - keys: capslock n
    outKeys: down`))
	require.NoError(t, err)
	ew := &writeToSlice{}
	err = manInTheMiddle(&logReader, ew, combos, true, true)
	require.True(t, errors.Is(err, io.EOF))
}

func Test_FJX_emits_f_but_should_not(t *testing.T) {
	log, err := os.ReadFile("testdata/fjx-emits-f-but-should-not.log")
	require.NoError(t, err)
	scanner := bufio.NewScanner(strings.NewReader(string(log)))
	logReader := ComboLogEventReader{scanner: scanner}
	combos, err := LoadYamlFromBytes([]byte(`
combos:
  - keys: f j
    outKeys: x`))
	require.NoError(t, err)
	ew := &writeToSlice{}
	err = manInTheMiddle(&logReader, ew, combos, true, true)
	require.True(t, errors.Is(err, io.EOF))
	ew.requireEqual(t, `
        	        	X-down
       	            	X-up
       	            	X-down
       	            	X-up
       	            	X-down
       	            	X-up
       	            	X-down
       	            	X-up
       	            	X-down
       	            	X-up
       	            	X-down
       	            	X-up
       	            	X-down
       	            	X-up
       	            	X-down
       	            	X-up
       	            	X-down
       	            	X-up
						X-down
        	            X-up
        	            X-down
        	            X-up
						`)
}
