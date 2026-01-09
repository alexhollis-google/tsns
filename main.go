package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	// How often the informer will re-process the entire local cache
	// to ensure the file is up to date.
	resyncPeriod = 30 * time.Second
)

var namespace, service, nodesFile string
var apiPort, peerPort int

func main() {
	flag.StringVar(&namespace, "namespace", "typesense", "The namespace that Typesense is installed within")
	flag.StringVar(&service, "service", "ts", "The name of the Typesense service to use the endpoints of")
	flag.StringVar(&nodesFile, "nodes-file", "/usr/share/typesense/nodes", "The location of the file to write node information to")
	flag.IntVar(&peerPort, "peer-port", 8107, "Port on which Typesense peering service listens")
	flag.IntVar(&apiPort, "api-port", 8108, "Port on which Typesense API service listens")
	flag.Parse()

	configPath := filepath.Join(homedir.HomeDir(), ".kube", "config")

	var config *rest.Config
	var err error

	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		// No config file found, fall back to in-cluster config.
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Fatalf("failed to build local config: %s\n", err)
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			log.Fatalf("failed to build in-cluster config: %s\n", err)
		}
	}

	clients, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("failed to create kubernetes client: %s\n", err)
	}

	log.Printf("Configured to watch namespace: %s", namespace)

	factory := informers.NewSharedInformerFactoryWithOptions(
		clients,
		resyncPeriod,
		informers.WithNamespace(namespace),
	)
	esInformer := factory.Discovery().V1().EndpointSlices()

	esInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			log.Println("Event: Add detected")
			writeToFile(esInformer.Lister(), nodesFile)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// In a real app, you might check if the ResourceVersion changed,
			// but for syncing to a file, we just write it out.
			log.Println("Event: Update/Resync detected")
			writeToFile(esInformer.Lister(), nodesFile)
		},
		DeleteFunc: func(obj interface{}) {
			log.Println("Event: Delete detected")
			writeToFile(esInformer.Lister(), nodesFile)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("Starting EndpointSlice Watcher...")
	factory.Start(ctx.Done())

	log.Println("Waiting for cache to sync...")
	if !cache.WaitForCacheSync(ctx.Done(), esInformer.Informer().HasSynced) {
		log.Fatal("Timed out waiting for caches to sync")
	}
	log.Println("Cache synced. Watching for changes...")

	writeToFile(esInformer.Lister(), nodesFile)

	waitForShutdown(cancel)
}

func writeToFile(lister interface{}, filename string) {
	var nodes []string
	slices, err := lister.(interface {
		List(selector labels.Selector) (ret []*discoveryv1.EndpointSlice, err error)
	}).List(labels.SelectorFromSet(labels.Set{discoveryv1.LabelServiceName: service}))

	if err != nil {
		log.Printf("Error listing endpoint slices from cache: %v", err)
		return
	}

	for _, es := range slices {
		fmt.Printf("  EndpointSlice Name: %s\n", es.Name)
		if es.OwnerReferences[len(es.OwnerReferences)-1].Name != service {
			continue
		}

		for _, endpoint := range es.Endpoints {
			fmt.Printf("    Endpoint Addresses: %v\n", endpoint.Addresses)
			for _, addr := range endpoint.Addresses {
				nodes = append(nodes, fmt.Sprintf("%s:%d:%d", addr, peerPort, apiPort))
				fmt.Printf("	Nodes: %v\n", nodes)
			}

		}
	}
	typesenseNodes := strings.Join(nodes, ",")

	if len(nodes) != 0 {
		log.Printf("New %d node configuration: %s\n", len(nodes), typesenseNodes)
	}

	tmpFile := filename + ".tmp"
	err = ioutil.WriteFile(tmpFile, []byte(typesenseNodes), 0644)
	if err != nil {
		log.Printf("Error writing temp file: %v", err)
		return
	}

	err = os.Rename(tmpFile, filename)
	if err != nil {
		log.Printf("Error renaming file to final destination: %v", err)
		return
	}

	log.Printf("Successfully updated %s with %d EndpointSlices", filename, len(slices))
}

func waitForShutdown(cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down...")
	cancel()
}
