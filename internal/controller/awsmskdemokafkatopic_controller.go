/*
Copyright 2025 NTT DATA.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/segmentio/kafka-go"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	awsCfg "github.com/aws/aws-sdk-go-v2/config"
	awsKafka "github.com/aws/aws-sdk-go-v2/service/kafka"
	// awsKafkaTypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	// corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	awsv1alpha1 "github.com/NTTDATA-DACH/k8s-operator-aws-msk-demo/api/v1alpha1"
)

const awsmskdemoinstanceFinalizer = "aws.nttdata.com/finalizer"

// AwsMSKDemoKafkaTopicReconciler reconciles a AwsMSKDemoKafkaTopic object
type AwsMSKDemoKafkaTopicReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	kafkaClient *awsKafka.Client
}

// +kubebuilder:rbac:groups=aws.nttdata.com,resources=awsmskdemokafkatopics,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aws.nttdata.com,resources=awsmskdemokafkatopics/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aws.nttdata.com,resources=awsmskdemokafkatopics/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the AwsMSKDemoKafkaTopic object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *AwsMSKDemoKafkaTopicReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info(fmt.Sprintf("reconcile triggered for '%s' in namespace '%s'...", req.Name, req.Namespace))

	// Fetch the AwsMSKDemoKafkaTopic topic
	topic := &awsv1alpha1.AwsMSKDemoKafkaTopic{}
	if err := r.Get(ctx, req.NamespacedName, topic); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found AwsMSKDemoKafkaTopic, exiting reconciliation loop...")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log.Info("found: " + topic.Name)

	// Create Kafka client
	cfg, err := awsCfg.LoadDefaultConfig(ctx)
	if err != nil {
		log.Error(err, "failed to load AWS config: "+err.Error())
		return ctrl.Result{}, err
	}
	r.kafkaClient = awsKafka.NewFromConfig(cfg)

	brokers, err := r.getMSKClusterBrokers(ctx, topic.Spec.ClusterArn)
	if err != nil {
		log.Error(err, "failed to get cluster brokers: "+err.Error())
		return ctrl.Result{}, err
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(topic, awsmskdemoinstanceFinalizer) {
		log.Info("adding finalizer for AwsMSKDemoKafkaTopic")
		topic.Status.Status = awsv1alpha1.StateUpdating

		if ok := controllerutil.AddFinalizer(topic, awsmskdemoinstanceFinalizer); !ok {
			err = fmt.Errorf("failed to add finalizer into AwsMSKDemoKafkaTopic")
			log.Error(err, "failed to add finalizer into AwsMSKDemoKafkaTopic")
			return ctrl.Result{}, err
		}
		if err := r.Update(ctx, topic); err != nil {
			log.Error(err, "failed to update AwsMSKDemoKafkaTopic to add finalizer: "+err.Error())
			return ctrl.Result{}, err
		}

		log.Info("added finalizer for AwsMSKDemoKafkaTopic")
		topic.Status.Status = awsv1alpha1.StateUpdated
	}

	// Remove instance with finalizer
	if !topic.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(topic, awsmskdemoinstanceFinalizer) {
			log.Info("starting to delete topic: " + topic.Spec.Name)
			topic.Status.Status = awsv1alpha1.StateDeleting

			if err := r.deleteMSKKafkaTopic(ctx, brokers, topic); err != nil {
				log.Error(err, "failed to remove topic: "+err.Error())
				return ctrl.Result{}, err
			}
			log.Info("topic deleted, removing finalizer")
			if ok := controllerutil.RemoveFinalizer(topic, awsmskdemoinstanceFinalizer); !ok {
				err = fmt.Errorf("failed to remove finalizer from AwsMSKDemoKafkaTopic")
				log.Error(err, "failed to remove finalizer from AwsMSKDemoKafkaTopic")
				return ctrl.Result{}, err
			}
			if err := r.Update(ctx, topic); err != nil {
				log.Error(err, "failed to update AwsMSKDemoKafkaTopic to remove finalizer: "+err.Error())
				return ctrl.Result{}, err
			}

			log.Info("finalizer deleted")
			topic.Status.Status = awsv1alpha1.StateDeleted
		}
	} else {
		err = r.createMSKKafkaTopic(ctx, brokers, topic)
		if err != nil {
			log.Error(err, "failed to create topic: "+topic.Spec.Name)
			return ctrl.Result{}, err
		}
	}

	log.Info("reconcile finished...")
	return ctrl.Result{}, nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) createMSKKafkaTopic(ctx context.Context, brokers []string, topic *awsv1alpha1.AwsMSKDemoKafkaTopic) error {
	log := log.FromContext(ctx)
	log.Info("trying to create topic: " + topic.Spec.Name)

	// Create dialer
	dialer, err := r.createDialerConfig(ctx)
	if err != nil {
		return err
	}

	// Create connection
	broker := brokers[0]
	log.Info("trying to dial broker: " + broker)
	conn, err := dialer.Dial("tcp", broker)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Create topic
	err = conn.CreateTopics(kafka.TopicConfig{
		Topic:             topic.Spec.Name,
		NumPartitions:     int(topic.Spec.Partitions),
		ReplicationFactor: int(topic.Spec.ReplicationFactor),
	})
	if err != nil {
		return err
	}

	log.Info("topic created: " + topic.Spec.Name)
	return nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) deleteMSKKafkaTopic(ctx context.Context, brokers []string, topic *awsv1alpha1.AwsMSKDemoKafkaTopic) error {
	log := log.FromContext(ctx)
	log.Info("trying to delete topic: " + topic.Spec.Name)

	// Create dialer
	dialer, err := r.createDialerConfig(ctx)
	if err != nil {
		return err
	}

	// Create connection
	broker := brokers[0]
	log.Info("trying to dial broker: " + broker)
	conn, err := dialer.Dial("tcp", broker)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Delete topic
	err = conn.DeleteTopics(topic.Spec.Name)
	if err != nil {
		return err
	}

	log.Info("topic deleted: " + topic.Spec.Name)
	return nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) getMSKClusterBrokers(ctx context.Context, clusterArn string) ([]string, error) {
	log := log.FromContext(ctx)
	log.Info("trying to retrieve cluster brokers for cluster: " + clusterArn)

	result, err := r.kafkaClient.DescribeClusterV2(ctx, &awsKafka.DescribeClusterV2Input{
		ClusterArn: aws.String(clusterArn),
	})
	if err != nil {
		return nil, err
	}
	clusterName := *result.ClusterInfo.ClusterName
	log.Info("found cluster based on arn with the name: " + clusterName)

	output, err := r.kafkaClient.GetBootstrapBrokers(ctx, &awsKafka.GetBootstrapBrokersInput{
		ClusterArn: aws.String(clusterArn),
	})
	if err != nil {
		return nil, err
	}
	var brokers *string
	if b := output.BootstrapBrokerString; b != nil && len(*b) > 0 {
		log.Info("BootstrapBrokerString found")
		brokers = b
	} else if b := output.BootstrapBrokerStringTls; b != nil && len(*b) > 0 {
		log.Info("BootstrapBrokerStringTls found")
		brokers = b
	} else if b := output.BootstrapBrokerStringSaslIam; b != nil && len(*b) > 0 {
		log.Info("BootstrapBrokerStringSaslIam found")
		brokers = b
	} else {
		return nil, fmt.Errorf("no bootstrap brokers returned for cluster: %s", clusterName)
	}

	log.Info("retrieved following cluster brokers: " + *brokers)
	return strings.Split(*brokers, ","), nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) createDialerConfig(ctx context.Context) (*kafka.Dialer, error) {
	// Load TLS certificates
	cert, err := r.loadCertificate(ctx)
	if err != nil {
		return nil, err
	}
	ca, err := r.loadRootCA(ctx)
	if err != nil {
		return nil, err
	}

	// Create dialer
	tlsCfg := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            ca,
		InsecureSkipVerify: true,
	}
	dialer := &kafka.Dialer{
		TLS: tlsCfg,
	}

	return dialer, nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) loadCertificate(ctx context.Context) (tls.Certificate, error) {
	log := log.FromContext(ctx)
	log.Info("trying to load certificate")

	cert, err := tls.LoadX509KeyPair("/certs/client.crt", "/certs/client.key")
	if err != nil {
		return tls.Certificate{}, err
	}

	log.Info("certificate loaded")
	return cert, nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) loadRootCA(ctx context.Context) (*x509.CertPool, error) {
	log := log.FromContext(ctx)
	log.Info("trying to load root ca")

	caCert, err := os.ReadFile("/certs/ca.crt")
	if err != nil {
		return nil, err
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, err
	}

	log.Info("root ca loaded")
	return caCertPool, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AwsMSKDemoKafkaTopicReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&awsv1alpha1.AwsMSKDemoKafkaTopic{}).
		Complete(r)
}
