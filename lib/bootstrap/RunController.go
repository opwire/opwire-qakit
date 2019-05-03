package bootstrap

import (
	"flag"
	"fmt"
	"testing"
	"time"
	"github.com/opwire/opwire-testa/lib/format"
	"github.com/opwire/opwire-testa/lib/engine"
	"github.com/opwire/opwire-testa/lib/script"
	"github.com/opwire/opwire-testa/lib/tag"
)

type RunControllerOptions interface {
	script.Source
	GetConfigPath() string
	GetNoColor() bool
}

type RunController struct {
	scriptLoader *script.Loader
	scriptSelector *script.Selector
	scriptSource script.Source
	tagManager *tag.Manager
	specHandler *engine.SpecHandler
	outputPrinter *format.OutputPrinter
	counter struct{
		Pending int
		Skipped int
		Success int
		Failure int
		Cracked int
	}
}

func NewRunController(opts RunControllerOptions) (r *RunController, err error) {
	r = &RunController{}

	// testing temporary storage
	r.scriptSource, err = script.NewSource(opts)
	if err != nil {
		return nil, err
	}

	// create a Script Loader instance
	r.scriptLoader, err = script.NewLoader(r.scriptSource)
	if err != nil {
		return nil, err
	}

	// create a Script Selector instance
	r.scriptSelector, err = script.NewSelector(r.scriptSource)
	if err != nil {
		return nil, err
	}

	// create a Manager instance
	r.tagManager, err = tag.NewManager(r.scriptSource)
	if err != nil {
		return nil, err
	}

	// create a Spec Handler instance
	r.specHandler, err = engine.NewSpecHandler(r.scriptSource)
	if err != nil {
		return nil, err
	}

	// create a OutputPrinter instance
	r.outputPrinter, err = format.NewOutputPrinter(opts)
	if err != nil {
		return nil, err
	}

	return r, nil
}

type RunArguments interface {}

func (r *RunController) Execute(args RunArguments) error {
	flag.Set("test.v", "false")

	// start time
	startTime := time.Now()

	// begin environments
	r.outputPrinter.Println()
	r.outputPrinter.Println(r.outputPrinter.Heading("Context"))
	printScriptSourceArgs(r.outputPrinter, r.scriptSource, r.scriptSelector, r.tagManager)

	// begin prerequisites
	r.outputPrinter.Println()
	r.outputPrinter.Println(r.outputPrinter.Heading("Loading"))

	// load test specifications
	descriptors := r.scriptLoader.Load()

	// filter invalid descriptors and display errors
	descriptors, rejected := filterInvalidDescriptors(descriptors)
	for _, d := range rejected {
		r.outputPrinter.Println(r.outputPrinter.TestSuiteTitle(d.Locator.RelativePath))
		r.outputPrinter.Println(r.outputPrinter.Section(d.Error.Error()))
	}

	// begin testing
	r.outputPrinter.Println()
	r.outputPrinter.Println(r.outputPrinter.Heading("Testing"))

	// create the test runners
	internalTests, err2 := r.wrapTestSuites(descriptors)
	if err2 != nil {
		return err2
	}

	// summary
	internalTests = append(internalTests, testing.InternalTest{
		Name: "Summary",
		F: func(t *testing.T) {
			// summarize testing
			r.outputPrinter.Println()
			r.outputPrinter.Println(r.outputPrinter.Heading("Summary"))

			r.outputPrinter.Printf("[*] Pending: %d, Skipped: %d, Cracked: %d, Failed: %d, Passed: %d",
				r.counter.Pending, r.counter.Skipped, r.counter.Cracked, r.counter.Success, r.counter.Failure)
			r.outputPrinter.Println()

			// total elapsed time
			duration := time.Since(startTime)
			r.outputPrinter.Printf("[*] Total elapsed time: %s", duration.String())
			r.outputPrinter.Println()

			// endof testing
			r.outputPrinter.Println()
		},
	})

	// Run the tests
	testing.Main(defaultMatchString, internalTests, []testing.InternalBenchmark{}, []testing.InternalExample{})

	return nil
}

func defaultMatchString(pat, str string) (bool, error) {
	return true, nil
}

func (r *RunController) wrapTestSuites(descriptors map[string]*script.Descriptor) ([]testing.InternalTest, error) {
	if r.specHandler == nil {
		panic(fmt.Errorf("SpecHandler must not be nil"))
	}
	tests := make([]testing.InternalTest, 0)
	for _, descriptor := range descriptors {
		test, err := r.wrapDescriptor(descriptor)
		if err == nil {
			tests = append(tests, test)
		}
	}
	return tests, nil
}

func (r *RunController) wrapDescriptor(descriptor *script.Descriptor) (testing.InternalTest, error) {
	testsuite := descriptor.TestSuite
	if testsuite == nil {
		return testing.InternalTest{}, fmt.Errorf("TestSuite must not be nil")
	}
	return testing.InternalTest{
		Name: descriptor.Locator.RelativePath,
		F: func (t *testing.T) {
			r.outputPrinter.Println(r.outputPrinter.TestSuiteTitle(descriptor.Locator.RelativePath))
			tests := make([]testing.InternalTest, 0)
			for _, testcase := range testsuite.TestCases {
				tests = append(tests, r.wrapTestCase(testcase))
			}
			testing.RunTests(defaultMatchString, tests)
		},
	}, nil
}

func (r *RunController) wrapTestCase(testcase *engine.TestCase) (testing.InternalTest) {
	return testing.InternalTest{
		Name: testcase.Title,
		F: func (t *testing.T) {
			if testcase.Pending != nil && *testcase.Pending {
				r.outputPrinter.Println(r.outputPrinter.Pending(testcase.Title))
				r.counter.Pending += 1
				return
			}
			if !r.scriptSelector.IsMatched(testcase) {
				label := printUnmatchedPattern(r.outputPrinter, "unmatched")
				r.outputPrinter.Println(r.outputPrinter.Skipped(testcase.Title), label)
				r.counter.Skipped += 1
				return
			}
			active, mark := r.tagManager.IsActive(testcase.Tags)
			tagstr := printMarkedTags(r.outputPrinter, testcase.Tags, mark)
			if !active {
				r.outputPrinter.Println(r.outputPrinter.Skipped(testcase.Title), tagstr)
				r.counter.Skipped += 1
				return
			}
			result, err := r.specHandler.Examine(testcase)
			exectime := printDuration(r.outputPrinter, result.Duration)
			if err != nil {
				r.outputPrinter.Println(r.outputPrinter.Cracked(testcase.Title), tagstr, exectime)
				r.counter.Cracked += 1
				return
			}
			if result != nil && len(result.Errors) > 0 {
				r.outputPrinter.Println(r.outputPrinter.Failure(testcase.Title), tagstr, exectime)
				for key, err := range result.Errors {
					r.outputPrinter.Printf(r.outputPrinter.SectionTitle(key))
					r.outputPrinter.Printf(r.outputPrinter.Section(err.Error()))
					r.outputPrinter.Println()
				}
				r.counter.Failure += 1
				return
			}
			r.outputPrinter.Println(r.outputPrinter.Success(testcase.Title), tagstr, exectime)
			r.counter.Success += 1
		},
	}
}
