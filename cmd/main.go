package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/api"
	"github.com/ochestra-tech/kubecostguard/internal/config"
	"github.com/ochestra-tech/kubecostguard/internal/cost"
	"github.com/ochestra-tech/kubecostguard/internal/health"
	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"
	"github.com/ochestra-tech/kubecostguard/internal/optimization"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down...", sig)
		cancel()
	}()

	// Initialize Kubernetes client
	k8sClient, err := kubernetes.NewClient(kubernetes.Config{
		// Add necessary fields from cfg.Kubernetes
		Kubeconfig: cfg.Kubernetes.Kubeconfig,
		InCluster:  cfg.Kubernetes.InCluster,
	})
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Initialize components
	healthMonitor := health.NewMonitor(ctx, k8sClient, cfg.Health)
	costAnalyzer := cost.NewAnalyzer(ctx, k8sClient, cfg.Cost)
	optimizer := optimization.NewOptimizer(ctx, k8sClient, costAnalyzer, cfg.Optimization)

	// Start health monitoring
	if err := healthMonitor.Start(); err != nil {
		log.Fatalf("Failed to start health monitor: %v", err)
	}

	// Start cost analysis
	if err := costAnalyzer.Start(); err != nil {
		log.Fatalf("Failed to start cost analyzer: %v", err)
	}

	// Start optimization engine
	if err := optimizer.Start(); err != nil {
		log.Fatalf("Failed to start optimizer: %v", err)
	}

	// Start API server
	server := api.NewServer(cfg.API, healthMonitor, costAnalyzer, optimizer)
	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("API server error: %v", err)
		}
	}()

	// Wait for context cancellation (shutdown signal)
	<-ctx.Done()

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	log.Println("Shutting down API server...")
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("API server shutdown error: %v", err)
	}

	log.Println("Shutting down health monitor...")
	healthMonitor.Stop()

	log.Println("Shutting down cost analyzer...")
	costAnalyzer.Stop()

	log.Println("Shutting down optimizer...")
	optimizer.Stop()

	log.Println("Shutdown complete")
}
