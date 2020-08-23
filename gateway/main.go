package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/depscloud/api/swagger"
	"github.com/depscloud/api/v1alpha/extractor"
	"github.com/depscloud/api/v1alpha/tracker"
	"github.com/depscloud/depscloud/gateway/internal/checks"
	"github.com/depscloud/depscloud/gateway/internal/proxies"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"

	"github.com/rs/cors"

	"github.com/sirupsen/logrus"

	"github.com/urfave/cli/v2"

	"golang.org/x/net/context"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	_ "google.golang.org/grpc/health"
)

// https://github.com/grpc/grpc/blob/master/doc/service_config.md
const serviceConfigTemplate = `{
	"loadBalancingPolicy": "%s",
	"healthCheckConfig": {
		"serviceName": ""
	}
}`

func dial(target, certFile, keyFile, caFile, lbPolicy string) (*grpc.ClientConn, error) {
	serviceConfig := fmt.Sprintf(serviceConfigTemplate, lbPolicy)

	dialOptions := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(serviceConfig),
	}

	if len(certFile) > 0 {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}

		certPool := x509.NewCertPool()
		bs, err := ioutil.ReadFile(caFile)
		if err != nil {
			return nil, err
		}

		ok := certPool.AppendCertsFromPEM(bs)
		if !ok {
			return nil, fmt.Errorf("failed to append certs")
		}

		transportCreds := credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{certificate},
			RootCAs:      certPool,
		})

		dialOptions = append(dialOptions, grpc.WithTransportCredentials(transportCreds))
	} else {
		dialOptions = append(dialOptions, grpc.WithInsecure())
	}

	return grpc.Dial(target, dialOptions...)
}

func dialExtractor(cfg *gatewayConfig) (*grpc.ClientConn, error) {
	return dial(cfg.extractorAddress,
		cfg.extractorCertPath, cfg.extractorKeyPath, cfg.extractorCAPath,
		cfg.extractorLBPolicy)
}

func dialTracker(cfg *gatewayConfig) (*grpc.ClientConn, error) {
	return dial(cfg.trackerAddress,
		cfg.trackerCertPath, cfg.trackerKeyPath, cfg.trackerCAPath,
		cfg.trackerLBPolicy)
}

type gatewayConfig struct {
	port int

	extractorAddress  string
	extractorCertPath string
	extractorKeyPath  string
	extractorCAPath   string
	extractorLBPolicy string

	trackerAddress  string
	trackerCertPath string
	trackerKeyPath  string
	trackerCAPath   string
	trackerLBPolicy string

	tlsKeyPath  string
	tlsCertPath string
	tlsCAPath   string
}

func main() {
	cfg := &gatewayConfig{
		port: 8080,

		extractorAddress:  "extractor:8090",
		extractorCertPath: "",
		extractorKeyPath:  "",
		extractorCAPath:   "",
		extractorLBPolicy: "round_robin",

		trackerAddress:  "tracker:8090",
		trackerCertPath: "",
		trackerKeyPath:  "",
		trackerCAPath:   "",
		trackerLBPolicy: "round_robin",

		tlsCertPath: "",
		tlsKeyPath:  "",
		tlsCAPath:   "",
	}

	app := &cli.App{
		Name:  "gateway",
		Usage: "an HTTP/gRPC proxy to backend services",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:        "port",
				Usage:       "the port to run on",
				Value:       cfg.port,
				Destination: &cfg.port,
				EnvVars:     []string{"HTTP_PORT"},
			},
			&cli.StringFlag{
				Name:        "extractor-address",
				Usage:       "address to the extractor service",
				Value:       cfg.extractorAddress,
				Destination: &cfg.extractorAddress,
				EnvVars:     []string{"EXTRACTOR_ADDRESS"},
			},
			&cli.StringFlag{
				Name:        "extractor-cert",
				Usage:       "certificate used to enable TLS for the extractor",
				Value:       cfg.extractorCertPath,
				Destination: &cfg.extractorCertPath,
				EnvVars:     []string{"EXTRACTOR_CERT_PATH"},
			},
			&cli.StringFlag{
				Name:        "extractor-key",
				Usage:       "key used to enable TLS for the extractor",
				Value:       cfg.extractorKeyPath,
				Destination: &cfg.extractorKeyPath,
				EnvVars:     []string{"EXTRACTOR_KEY_PATH"},
			},
			&cli.StringFlag{
				Name:        "extractor-ca",
				Usage:       "ca used to enable TLS for the extractor",
				Value:       cfg.extractorCAPath,
				Destination: &cfg.extractorCAPath,
				EnvVars:     []string{"EXTRACTOR_CA_PATH"},
			},
			&cli.StringFlag{
				Name:        "extractor-lb",
				Usage:       "the load balancer policy to use for the extractor",
				Value:       cfg.extractorLBPolicy,
				Destination: &cfg.extractorLBPolicy,
				EnvVars:     []string{"EXTRACTOR_LBPOLICY"},
			},
			&cli.StringFlag{
				Name:        "tracker-address",
				Usage:       "address to the tracker service",
				Value:       cfg.trackerAddress,
				Destination: &cfg.trackerAddress,
				EnvVars:     []string{"TRACKER_ADDRESS"},
			},
			&cli.StringFlag{
				Name:        "tracker-cert",
				Usage:       "certificate used to enable TLS for the tracker",
				Value:       cfg.trackerCertPath,
				Destination: &cfg.trackerCertPath,
				EnvVars:     []string{"TRACKER_CERT_PATH"},
			},
			&cli.StringFlag{
				Name:        "tracker-key",
				Usage:       "key used to enable TLS for the tracker",
				Value:       cfg.trackerKeyPath,
				Destination: &cfg.trackerKeyPath,
				EnvVars:     []string{"TRACKER_KEY_PATH"},
			},
			&cli.StringFlag{
				Name:        "tracker-ca",
				Usage:       "ca used to enable TLS for the tracker",
				Value:       cfg.trackerCAPath,
				Destination: &cfg.trackerCAPath,
				EnvVars:     []string{"TRACKER_CA_PATH"},
			},
			&cli.StringFlag{
				Name:        "tracker-lb",
				Usage:       "the load balancer policy to use for the tracker",
				Value:       cfg.trackerLBPolicy,
				Destination: &cfg.trackerLBPolicy,
				EnvVars:     []string{"TRACKER_LBPOLICY"},
			},
			&cli.StringFlag{
				Name:        "tls-key",
				Usage:       "path to the file containing the TLS private key",
				Value:       cfg.tlsKeyPath,
				Destination: &cfg.tlsKeyPath,
				EnvVars:     []string{"TLS_KEY_PATH"},
			},
			&cli.StringFlag{
				Name:        "tls-cert",
				Usage:       "path to the file containing the TLS certificate",
				Value:       cfg.tlsCertPath,
				Destination: &cfg.tlsCertPath,
				EnvVars:     []string{"TLS_CERT_PATH"},
			},
			&cli.StringFlag{
				Name:        "tls-ca",
				Usage:       "path to the file containing the TLS certificate authority",
				Value:       cfg.tlsCAPath,
				Destination: &cfg.tlsCAPath,
				EnvVars:     []string{"TLS_CA_PATH"},
			},
		},
		Action: func(c *cli.Context) error {
			grpcServer := grpc.NewServer()
			gatewayMux := runtime.NewServeMux()

			ctx := context.Background()

			extractorConn, err := dialExtractor(cfg)
			if err != nil {
				return err
			}
			defer extractorConn.Close()

			trackerConn, err := dialTracker(cfg)
			if err != nil {
				return err
			}
			defer trackerConn.Close()

			sourceService := tracker.NewSourceServiceClient(trackerConn)
			tracker.RegisterSourceServiceServer(grpcServer, proxies.NewSourceServiceProxy(sourceService))
			_ = tracker.RegisterSourceServiceHandlerClient(ctx, gatewayMux, sourceService)

			moduleService := tracker.NewModuleServiceClient(trackerConn)
			tracker.RegisterModuleServiceServer(grpcServer, proxies.NewModuleServiceProxy(moduleService))
			_ = tracker.RegisterModuleServiceHandlerClient(ctx, gatewayMux, moduleService)

			dependencyService := tracker.NewDependencyServiceClient(trackerConn)
			tracker.RegisterDependencyServiceServer(grpcServer, proxies.NewDependencyServiceProxy(dependencyService))
			_ = tracker.RegisterDependencyServiceHandlerClient(ctx, gatewayMux, dependencyService)

			extractorService := extractor.NewDependencyExtractorClient(extractorConn)
			extractor.RegisterDependencyExtractorServer(grpcServer, proxies.NewExtractorServiceProxy(extractorService))
			_ = extractor.RegisterDependencyExtractorHandlerClient(ctx, gatewayMux, extractorService)

			searchService := tracker.NewSearchServiceClient(trackerConn)
			tracker.RegisterSearchServiceServer(grpcServer, proxies.NewSearchServiceProxy(searchService))

			httpMux := http.NewServeMux()

			httpMux.HandleFunc("/swagger/", func(writer http.ResponseWriter, request *http.Request) {
				assetPath := strings.TrimPrefix(request.URL.Path, "/swagger/")

				if len(assetPath) == 0 {
					if err := json.NewEncoder(writer).Encode(swagger.AssetNames()); err != nil {
						writer.WriteHeader(500)
					} else {
						writer.WriteHeader(200)
					}
					return
				}

				asset, err := swagger.Asset(assetPath)
				if err != nil {
					writer.WriteHeader(404)
					return
				}

				writer.WriteHeader(200)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write(asset)
			})

			// setup /healthz

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			allChecks := checks.Checks(extractorService, sourceService, moduleService)
			checks.RegisterHealthCheck(ctx, httpMux, grpcServer, allChecks)

			// set up root

			httpMux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
				if request.ProtoMajor == 2 &&
					strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc") {
					grpcServer.ServeHTTP(writer, request)
				} else {
					gatewayMux.ServeHTTP(writer, request)
				}
			})

			// set up edge mux

			h2cMux := h2c.NewHandler(httpMux, &http2.Server{})

			apiMux := cors.Default().Handler(h2cMux)

			address := fmt.Sprintf(":%d", cfg.port)
			if len(cfg.tlsCertPath) > 0 && len(cfg.tlsKeyPath) > 0 && len(cfg.tlsCAPath) > 0 {
				certificate, err := tls.LoadX509KeyPair(cfg.tlsCertPath, cfg.tlsKeyPath)
				if err != nil {
					return err
				}

				certPool := x509.NewCertPool()
				bs, err := ioutil.ReadFile(cfg.tlsCAPath)
				if err != nil {
					return err
				}

				ok := certPool.AppendCertsFromPEM(bs)
				if !ok {
					return fmt.Errorf("failed to append certs")
				}

				listener, err := tls.Listen("tcp", address, &tls.Config{
					Certificates: []tls.Certificate{certificate},
					ClientAuth:   tls.RequireAndVerifyClientCert,
					ClientCAs:    certPool,
				})
				if err != nil {
					return err
				}

				logrus.Infof("[main] starting TLS server on %s", address)
				return http.Serve(listener, apiMux)
			}

			logrus.Infof("[main] starting plaintext server on %s", address)
			return http.ListenAndServe(address, apiMux)
		},
	}

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
