package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"contrib.go.opencensus.io/exporter/stackdriver"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/api/monitoredres"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/profiler"

	//"contrib.go.opencensus.io/exporter/stackdriver/propagation"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	applog "github.com/salrashid123/stackdriver_istio_helloworld/minimal_gcp/applog"

	log "github.com/sirupsen/logrus"

	"go.opencensus.io/stats"
	"go.opencensus.io/tag"

	"go.opencensus.io/plugin/ochttp"
)

const ()

var (
	mCount     = stats.Int64("Count", "# number of called..", stats.UnitNone)
	keyPath, _ = tag.NewKey("path")
	countView  = &view.View{
		Name:        "demo/simplemeasure",
		Measure:     mCount,
		Description: "The count of calls per path",
		Aggregation: view.Count(),
		TagKeys:     []tag.Key{keyPath},
	}

	version = os.Getenv("VER")

	errorClient *errorreporting.Client
)

type CustomError struct {
	Code    int
	Message string
}

func NewCustomError(code int, message string) *CustomError {
	return &CustomError{
		Code:    code,
		Message: message,
	}
}
func (e *CustomError) Error() string {
	return e.Message
}

func trackVistHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			ctx, err := tag.New(context.Background(), tag.Insert(keyPath, r.URL.Path))
			if err != nil {
				log.Println(err)
			}
			stats.Record(ctx, mCount.M(1))
		}()
		next.ServeHTTP(w, r)
	})
}

func root(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func measure(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Set metric counter %v", time.Now().String())
}

func hostname(w http.ResponseWriter, r *http.Request) {
	var h, err = os.Hostname()
	if err != nil {
		log.Errorf("Unable to get hostname %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "hello from %v, i'm running version %v", h, version)
}

func logger(w http.ResponseWriter, r *http.Request) {
	log.Errorf("Error Level Log")
	log.Printf("Info Level Log")
	fmt.Fprintf(w, "you got logged")
}

func errorReporter(w http.ResponseWriter, r *http.Request) {

	err := NewCustomError(500, "Some random error")
	errorClient.Report(errorreporting.Entry{
		Error: err,
	})

	log.Errorf(err.Message)

	fmt.Fprintf(w, "Logged Some Error but its ok")
}

func delay(w http.ResponseWriter, r *http.Request) {

	var d int
	var err error
	delay := r.URL.Query().Get("delay")
	if delay == "" {
		d = rand.Intn(3000)
	} else {
		d, err = strconv.Atoi(delay)
		if err != nil {
			log.Infof("bad latency value, using default")
			d = 1000
		}
	}
	log.Printf("Slow down  %vms", d)
	time.Sleep(time.Duration(d) * time.Millisecond)
	fmt.Fprintf(w, "done")
}

func debugger(w http.ResponseWriter, r *http.Request) {
	param := r.URL.Query().Get("param")

	log.Printf("Passed parameter: %v", param)
	fmt.Fprintf(w, "done debugging")
}

func backend(w http.ResponseWriter, r *http.Request) {

	backendHost := os.Getenv("BE_SERVICE_HOST")
	backendPort := os.Getenv("BE_SERVICE_PORT")
	log.Printf("Found ENV lookup backend ip: %v port: %v\n", backendHost, backendPort)

	ctx := r.Context()
	hreq, _ := http.NewRequest("GET", fmt.Sprintf("http://%v:%v/backend", backendHost, backendPort), nil)
	hreq = hreq.WithContext(ctx)
	client := &http.Client{
		Transport: &ochttp.Transport{
			// Use Google Cloud propagation format.
			//Propagation: &propagation.HTTPFormat{},
		},
	}
	rr, err := client.Do(hreq)
	if err != nil {
		log.Printf("Unable to make backend requestt: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	f, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		log.Println(err)
	}
	rr.Body.Close()
	if err != nil {
		log.Println(err)
	}

	fmt.Fprintf(w, "Response from Backend: %v", string(f))
}

func tracer(w http.ResponseWriter, r *http.Request) {

	backendHost := os.Getenv("BE_SERVICE_HOST")
	backendPort := os.Getenv("BE_SERVICE_PORT")
	log.Printf("Found ENV lookup backend ip: %v port: %v\n", backendHost, backendPort)

	ctx := r.Context()

	//client := &http.Client{Transport: &ochttp.Transport{}}

	// start span
	sctx, sleepSpan := trace.StartSpan(ctx, "start=sleep_for_no_reason")

	applog.Printf(sctx, "somewhere in the main span...")

	time.Sleep(200 * time.Millisecond)
	sleepSpan.End()
	// end span

	// Make GCS api request

	tokenSource, err := google.DefaultTokenSource(oauth2.NoContext, storage.ScopeReadOnly)
	if err != nil {
		log.Printf("Unable to acquire token source: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	storeageCient, err := storage.NewClient(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		log.Printf("Unable to acquire storage Client: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	bkt := storeageCient.Bucket(os.Getenv("BUCKET_NAME"))
	obj := bkt.Object("some_file.txt")

	re, err := obj.NewReader(ctx)
	if err != nil {
		log.Printf("Unable to read file: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		defer re.Close()
		return
	}
	defer re.Close()
	// End GCS API call

	// Start Span
	_, fileSpan := trace.StartSpan(ctx, "start=print_file")

	if _, err := io.Copy(os.Stdout, re); err != nil {
		log.Printf("Unable to Print file: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	time.Sleep(50 * time.Millisecond)
	fileSpan.End()
	// End Span

	// Start span
	// https://cloud.google.com/trace/docs/setup/go#sample_application_for_go
	cc, span := trace.StartSpan(ctx, "start=requst_to_backend")
	client := &http.Client{
		Transport: &ochttp.Transport{
			// Use Google Cloud propagation format.
			//Propagation: &propagation.HTTPFormat{},
		},
	}

	hreq, _ := http.NewRequest("GET", fmt.Sprintf("http://%v:%v/tracer", backendHost, backendPort), nil)

	// add context to outbound http request
	hreq = hreq.WithContext(cc)
	rr, err := client.Do(hreq)
	if err != nil {
		log.Printf("Unable to make backend requestt: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	f, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		log.Println(err)
	}
	rr.Body.Close()
	if err != nil {
		log.Println(err)
	}
	span.End()
	fmt.Fprintf(w, "Response from Backend: %v", string(f))
}

func main() {

	ctx := context.Background()

	applog.Initialize(os.Getenv("GOOGLE_CLOUD_PROJECT"))
	defer applog.Close()

	// Start Logging to stdout as JSON
	log.SetFormatter(&log.JSONFormatter{
		DisableTimestamp: true,
		FieldMap: log.FieldMap{
			log.FieldKeyLevel: "severity",
		},
	})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	log.Infof("Starting version %v", version)
	// END logging to stdout

	// I know, this is a workaround for stackdriver:
	// https://github.com/GoogleCloudPlatform/microservices-demo/issues/199#issuecomment-493283992
	time.Sleep(3 * time.Second)
	if os.Getenv("DEBUG") == "1" {
		// START PROFILER
		log.Infof("Debug Enabled, starting profiler on version %v", version)
		if err := profiler.Start(profiler.Config{
			Service:        "fe",
			ServiceVersion: version,
			DebugLogging:   false,
			ProjectID:      os.Getenv("GOOGLE_CLOUD_PROJECT"),
		}); err != nil {
			log.Errorf("Unable to start Profiler %v", err)
		}
		// END Profiler
	}

	// START OC Confg for SD on GKE ####################
	instanceID, err := metadata.InstanceID()
	if err != nil {
		log.Errorf("Error getting instance ID:", err)
		instanceID = "unknown"
	}
	zone, err := metadata.Zone()
	if err != nil {
		log.Errorf("Error getting zone:", err)
		zone = "unknown"
	}

	log.Infof("Using zone=%v  instanceID=%v", zone, instanceID)

	sd, err := stackdriver.NewExporter(stackdriver.Options{
		ProjectID: os.Getenv("GOOGLE_CLOUD_PROJECT"),
		Resource: &monitoredres.MonitoredResource{
			Type: "gke_container",
			Labels: map[string]string{
				"project_id":     os.Getenv("GOOGLE_CLOUD_PROJECT"),
				"cluster_name":   os.Getenv("GKE_CLUSTER_NAME"),
				"instance_id":    instanceID,
				"zone":           zone,
				"namespace_id":   os.Getenv("MY_POD_NAMESPACE"),
				"pod_id":         os.Getenv("MY_POD_NAME"),
				"container_name": os.Getenv("MY_CONTAINER_NAME"),
			},
		},
		DefaultMonitoringLabels: &stackdriver.Labels{},
	})
	if err != nil {
		log.Fatal(err)
	}

	// END OC Confg for SD on GKE ####################

	// START OC Metrics ####################
	if err := view.Register(countView); err != nil {
		log.Fatal(err)
	}
	// Stackdriver reporitng must be min 60s!
	view.SetReportingPeriod(60 * time.Second)
	view.RegisterExporter(sd)

	// END OC Metrics ####################

	// END OC Trace ####################
	trace.ApplyConfig(trace.Config{
		DefaultSampler: trace.AlwaysSample(),
	})
	trace.RegisterExporter(sd)
	// END OC Trace ####################

	// Setup ErrorReporting

	errorClient, err = errorreporting.NewClient(ctx, os.Getenv("GOOGLE_CLOUD_PROJECT"), errorreporting.Config{
		ServiceName: "fe",
		OnError: func(err error) {
			log.Printf("Could not log error: %v", err)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer errorClient.Close()

	// END ErrorReporting

	rootHandler := http.HandlerFunc(root)
	hostnameHander := http.HandlerFunc(hostname)
	traceHandler := http.HandlerFunc(tracer)
	backendHandler := http.HandlerFunc(backend)
	logHandler := http.HandlerFunc(logger)
	delayHandler := http.HandlerFunc(delay)
	measureHandler := http.HandlerFunc(measure)
	debugHandler := http.HandlerFunc(debugger)
	errorHandler := http.HandlerFunc(errorReporter)

	http.Handle("/", rootHandler)
	http.Handle("/hostname", hostnameHander)
	http.Handle("/tracer", traceHandler)
	http.Handle("/backend", backendHandler)
	http.Handle("/log", logHandler)
	http.Handle("/delay", delayHandler)
	http.Handle("/error", errorHandler)
	http.Handle("/debug", debugHandler)
	http.Handle("/measure", trackVistHandler(measureHandler))

	log.Fatal(http.ListenAndServe(":8080", &ochttp.Handler{
		IsPublicEndpoint: false,
		GetStartOptions: func(r *http.Request) trace.StartOptions {
			startOptions := trace.StartOptions{}
			if r.UserAgent() == "GoogleHC/1.0" {
				startOptions.Sampler = trace.NeverSample()
			}
			return startOptions
		},
	}))

}
