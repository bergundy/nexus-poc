package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

type options struct {
	cloud            bool
	certsDir         string
	skipEnvSetup     bool
	callerNamespace  string
	handlerNamespace string
}

func getOptions(args []string) options {
	var opts options
	set := flag.NewFlagSet("nexus-poc", flag.ExitOnError)
	set.BoolVar(&opts.cloud, "cloud", false, "Run on cloud (default local)")
	set.StringVar(&opts.certsDir, "certs-dir", "/Users/bergundy/temporal/cloud-certs", "Run on cloud (default local)")
	set.BoolVar(&opts.skipEnvSetup, "skip-env-setup", false, "Skip env setup (namespace and service registration)")
	set.StringVar(&opts.callerNamespace, "caller-namespace", "nexus-poc-caller.temporal-dev", "Caller namespace (in cloud this should include the account ID)")
	set.StringVar(&opts.handlerNamespace, "handler-namespace", "nexus-poc-handler.temporal-dev", "Handler namespace (in cloud this should include the account ID)")

	if err := set.Parse(args); err != nil {
		log.Panic("failed parsing args:", err)
	}
	return opts
}

func createAdminClient(opts options) client.Client {
	if opts.cloud {
		return createCloudAdminClient(opts.certsDir)
	}
	adminClient, err := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "ignored",
	})
	if err != nil {
		log.Panic(err)
	}
	return adminClient
}

func createCloudAdminClient(certsDir string) client.Client {
	clientCert := filepath.Join(certsDir, "internode.crt")
	clientKey := filepath.Join(certsDir, "internode.key")
	serverRootCACert := filepath.Join(certsDir, "internode-ca.crt")

	cert, err := tls.LoadX509KeyPair(clientCert, clientKey)
	if err != nil {
		log.Panic(err)
	}
	serverCAPool := x509.NewCertPool()
	b, err := os.ReadFile(serverRootCACert)
	if err != nil {
		log.Panic("failed reading server CA:", err)
	} else if !serverCAPool.AppendCertsFromPEM(b) {
		log.Panic("server CA PEM file invalid")
	}
	adminClient, err := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "ignored",
		// Namespace: "nexus-poc-caller.temporal-dev",
		ConnectionOptions: client.ConnectionOptions{
			TLS: &tls.Config{
				Certificates: []tls.Certificate{cert},
				ServerName:   "frontend.temporal.svc.cluster.local",
				RootCAs:      serverCAPool,
			},
		},
	})
	if err != nil {
		log.Panic(err)
	}
	return adminClient
}

func createClients(opts options) (client.Client, client.Client) {
	if opts.cloud {
		return createCloudClients(opts)
	}
	callerClient, err := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: opts.callerNamespace,
	})
	if err != nil {
		log.Panic(err)
	}

	handlerClient, err := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: opts.handlerNamespace,
	})
	if err != nil {
		log.Panic(err)
	}
	return callerClient, handlerClient
}

func createCloudClients(opts options) (client.Client, client.Client) {
	clientCert := filepath.Join(opts.certsDir, "nexus-client.pem")
	clientKey := filepath.Join(opts.certsDir, "nexus-client.key")
	// Load client cert
	cert, err := tls.LoadX509KeyPair(clientCert, clientKey)
	if err != nil {
		log.Panic(err)
	}
	callerClient, err := client.Dial(client.Options{
		HostPort:  fmt.Sprintf("%s.tmprl-test.cloud:7233", opts.callerNamespace),
		Namespace: opts.callerNamespace,
		ConnectionOptions: client.ConnectionOptions{
			TLS: &tls.Config{
				Certificates: []tls.Certificate{cert},
				ServerName:   fmt.Sprintf("%s.tmprl-test.cloud", opts.callerNamespace),
			},
		},
	})
	if err != nil {
		log.Panic(err)
	}

	handlerClient, err := client.Dial(client.Options{
		HostPort:  fmt.Sprintf("%s.tmprl-test.cloud:7233", opts.handlerNamespace),
		Namespace: opts.handlerNamespace,
		ConnectionOptions: client.ConnectionOptions{
			TLS: &tls.Config{
				Certificates: []tls.Certificate{cert},
				ServerName:   fmt.Sprintf("%s.tmprl-test.cloud", opts.handlerNamespace),
			},
		},
	})
	if err != nil {
		log.Panic(err)
	}
	return callerClient, handlerClient
}

func setupEnv(ctx context.Context, opts options) {
	adminClient := createAdminClient(opts)
	if !opts.cloud {
		// Create the namespaces in the local cluster.
		// In cloud those need to be created with internal admin APIs and placed so they're placed on the PoC cluster.

		// Ignore namespace registration errors in case the namespaces already exists.
		// This whole step is just for convenience.
		rp := time.Hour * 24
		adminClient.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{
			Namespace:                        "nexus-poc-caller.temporal-dev",
			WorkflowExecutionRetentionPeriod: &rp,
		})
		adminClient.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{
			Namespace:                        "nexus-poc-handler.temporal-dev",
			WorkflowExecutionRetentionPeriod: &rp,
		})
	}

	// Register our service in the caller namespace's outbound registry.
	_, err := adminClient.WorkflowService().UpdateNamespace(ctx, &workflowservice.UpdateNamespaceRequest{
		Namespace: "nexus-poc-caller.temporal-dev",
		UpdateInfo: &namespace.UpdateNamespaceInfo{
			OutgoingServiceUpdates: []*namespace.OutgoingServiceUpdate{
				{Variant: &namespace.OutgoingServiceUpdate_CreateOrUpdateService_{
					CreateOrUpdateService: &namespace.OutgoingServiceUpdate_CreateOrUpdateService{
						Name: serviceName,
						// localhost is a placeholder here that tells the task processor to make a cluster local nexus call.
						// This experience will likely change.
						BaseUrl: "http://localhost:7253/" + serviceName,
					},
				}},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	d, err := adminClient.WorkflowService().DescribeNamespace(ctx, &workflowservice.DescribeNamespaceRequest{
		Namespace: "nexus-poc-caller.temporal-dev",
	})
	if err != nil {
		log.Fatal(err)
	}
	if d.NamespaceInfo.OutgoingServiceRegistry[serviceName].BaseUrl == "" {
		log.Fatal("outgoing service registry not updated", d.NamespaceInfo.OutgoingServiceRegistry)
	}

	// Register our service in the cluster's inbound registry and map it to the handler namespace and task queue.
	_, err = adminClient.OperatorService().CreateOrUpdateNexusIncomingService(ctx, &operatorservice.CreateOrUpdateNexusIncomingServiceRequest{
		NexusIncomingService: &operatorservice.NexusIncomingService{
			Name:      serviceName,
			Namespace: "nexus-poc-handler.temporal-dev",
			TaskQueue: "my-handler-queue",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	s, err := adminClient.OperatorService().GetNexusIncomingService(ctx, &operatorservice.GetNexusIncomingServiceRequest{
		Name: serviceName,
	})
	if err != nil {
		log.Fatal(err)
	}
	if s.NexusIncomingService.Name != serviceName {
		log.Fatal("incoming service registry not updated", s)
	}
}
