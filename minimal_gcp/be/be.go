package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"contrib.go.opencensus.io/exporter/stackdriver"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/api/monitoredres"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	log "github.com/sirupsen/logrus"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	applog "github.com/salrashid123/minimal_gcp/applog"
)

var (
	version = os.Getenv("VER")
)

func backend(w http.ResponseWriter, r *http.Request) {
	log.Infof("...backend version %v called on %v", version, os.Getenv("MY_CONTAINER_NAME"))
	if version == "2" {
		log.Infof("...just doing nothing for... 1000ms")
		time.Sleep(time.Duration(1000) * time.Millisecond)
	}
	fmt.Fprintf(w, "This is a response from Backend %v running version %v", os.Getenv("MY_POD_NAME"), version)
}

func tracer(w http.ResponseWriter, r *http.Request) {

	// Acquire inbound context
	ctx := r.Context()

	// Start Span
	_, sleepSpan := trace.StartSpan(ctx, "start=start_sleep_backend")

	sctx, sleepSpan := trace.StartSpan(ctx, "start=sleep_for_no_reason")

	applog.Printf(sctx, "somewhere in the BACKEND span...")

	if version == "2" {
		log.Infof("...just doing nothing for... 1000ms")
		time.Sleep(time.Duration(1000) * time.Millisecond)
	}
	sleepSpan.End()
	// End Span

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

	fmt.Fprintf(w, "This is a response from Backend %v", os.Getenv("MY_POD_NAME"))
}

func main() {

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
	// END logging to stdout

	// START OC Confg for SD on GKE ####################
	time.Sleep(3 * time.Second)
	instanceID, err := metadata.InstanceID()
	if err != nil {
		log.Println("Error getting instance ID:", err)
		instanceID = "unknown"
		log.Fatal(err)
	}
	zone, err := metadata.Zone()
	if err != nil {
		log.Println("Error getting zone:", err)
		zone = "unknown"
		log.Fatal(err)
	}

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

	// END OC Trace ####################
	trace.ApplyConfig(trace.Config{
		DefaultSampler: trace.AlwaysSample(),
	})
	trace.RegisterExporter(sd)
	// END OC Trace ####################

	http.HandleFunc("/tracer", tracer)
	http.HandleFunc("/backend", backend)
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
