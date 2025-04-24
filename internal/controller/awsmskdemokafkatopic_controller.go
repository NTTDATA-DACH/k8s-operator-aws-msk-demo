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
	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/aws"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	awsCfg "github.com/aws/aws-sdk-go-v2/config"
	awsKafka "github.com/aws/aws-sdk-go-v2/service/kafka"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	awsv1alpha1 "github.com/NTTDATA-DACH/k8s-operator-aws-msk-demo/api/v1alpha1"
)

const (
	awsmskdemoinstanceFinalizer = "aws.nttdata.com/finalizer"
	sslKeyLocation              = "/certs/client.key"
	sslCertLocation             = "/certs/client.crt"
	sslCALocation               = "/certs/ca.crt"
)

// AwsMSKDemoKafkaTopicReconciler reconciles a AwsMSKDemoKafkaTopic object
type AwsMSKDemoKafkaTopicReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	kafkaClient  *awsKafka.Client
	clusterAdmin sarama.ClusterAdmin
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

	// Find brokers
	brokers, err := r.getMSKClusterBrokers(ctx, topic.Spec.ClusterArn)
	if err != nil {
		log.Error(err, "failed to get cluster brokers: "+err.Error())
		return ctrl.Result{}, err
	}

	// Create cluster admin client
	saramaCfg := sarama.NewConfig()
	saramaCfg.Version = sarama.V3_6_0_0 // for PoC purposes only

	tslCfg, err := r.createTlsConfig(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	saramaCfg.Net.TLS.Enable = true
	saramaCfg.Net.TLS.Config = tslCfg
	r.clusterAdmin, err = sarama.NewClusterAdmin(brokers, saramaCfg)
	log.Info("cluster admin created")

	if !topic.DeletionTimestamp.IsZero() {
		topic.Status.Status = awsv1alpha1.StateDeleting

		// Remove ACLs
		if err = r.removeKafkaACLs(ctx, topic.Spec.ACLs); err != nil {
			log.Error(err, "failed to apply ACL: "+err.Error())
			return ctrl.Result{}, err
		}

		// Remove topic
		if err = r.deleteMSKKafkaTopic(ctx, topic); err != nil {
			log.Error(err, "failed to delete topic: "+err.Error())
			return ctrl.Result{}, err
		}

		// Remove finalizer
		if controllerutil.ContainsFinalizer(topic, awsmskdemoinstanceFinalizer) {
			if err = r.removeFinalizer(ctx, topic); err != nil {
				log.Error(err, "failed to remove finalizer: "+err.Error())
				return ctrl.Result{}, err
			}
		}

		topic.Status.Status = awsv1alpha1.StateDeleted
	} else {
		topic.Status.Status = awsv1alpha1.StateCreating

		// Add finalizer
		if !controllerutil.ContainsFinalizer(topic, awsmskdemoinstanceFinalizer) {
			log.Info("adding finalizer for AwsMSKDemoKafkaTopic")

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
		}

		// Create topic
		err = r.createOrUpdateMSKKafkaTopic(ctx, topic)
		if err != nil {
			log.Error(err, "failed to create topic: "+topic.Spec.Name)
			return ctrl.Result{}, err
		}
		err = r.applyKafkaACLs(ctx, topic.Spec.ACLs)
		if err != nil {
			log.Error(err, "failed to apply ACLs: "+err.Error())
		}
		topic.Status.Status = awsv1alpha1.StateCreated
	}

	log.Info("reconcile finished...")
	return ctrl.Result{}, nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) createOrUpdateMSKKafkaTopic(ctx context.Context, topic *awsv1alpha1.AwsMSKDemoKafkaTopic) error {
	log := log.FromContext(ctx)

	topics, err := r.clusterAdmin.DescribeTopics([]string{topic.Spec.Name})
	if err != nil {
		// Topic not found or error, try to create
		log.Info("trying to create topic: " + topic.Spec.Name)
		detail := &sarama.TopicDetail{
			NumPartitions:     topic.Spec.Partitions,
			ReplicationFactor: topic.Spec.ReplicationFactor,
		}
		if err = r.clusterAdmin.CreateTopic(topic.Spec.Name, detail, false); err != nil {
			log.Error(err, "create topic failed: "+err.Error())
			return err
		}
		log.Info("topic created: " + topic.Spec.Name)
	} else {
		// Topic exists, try to update
		log.Info("trying to update topic: " + topic.Spec.Name)
		existingPartitions := int32(len(topics[0].Partitions))
		if topic.Spec.Partitions > existingPartitions {
			if err = r.clusterAdmin.CreatePartitions(topic.Spec.Name, topic.Spec.Partitions, nil, false); err != nil {
				log.Error(err, "updating partitions failed: "+err.Error())
				return err
			}
		}
		log.Info("topic updated: " + topic.Spec.Name)
	}

	return nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) applyKafkaACLs(ctx context.Context, acls []awsv1alpha1.AwsMSKDemoKafkaACL) error {
	log := log.FromContext(ctx)
	log.Info("trying to apply acls")

	for _, acl := range acls {
		pt, op := r.getACLPermissionTypeAndOperation(acl)
		log.Info(fmt.Sprintf("got acl for topic %s: %s for %s", acl.TopicName, pt.String(), op.String()))

		res := sarama.Resource{
			ResourceType:        sarama.AclResourceTopic,
			ResourceName:        acl.TopicName,
			ResourcePatternType: sarama.AclPatternLiteral,
		}
		entry := sarama.Acl{
			Principal:      acl.Principal,
			PermissionType: pt,
			Operation:      op,
		}
		err := r.clusterAdmin.CreateACL(res, entry)
		if err != nil {
			log.Error(err, "failed to apply ACL: "+err.Error())
			return err
		}
	}

	log.Info("acls applied")
	return nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) removeKafkaACLs(ctx context.Context, acls []awsv1alpha1.AwsMSKDemoKafkaACL) error {
	log := log.FromContext(ctx)
	log.Info("trying to apply acls")

	for _, acl := range acls {
		pt, op := r.getACLPermissionTypeAndOperation(acl)
		log.Info(fmt.Sprintf("got acl for topic %s: %s for %s", acl.TopicName, pt.String(), op.String()))

		filter := sarama.AclFilter{
			Principal:      &acl.Principal,
			Operation:      op,
			PermissionType: pt,
			ResourceName:   &acl.TopicName,
			ResourceType:   sarama.AclResourceTopic,
		}
		_, err := r.clusterAdmin.DeleteACL(filter, false)
		if err != nil {
			log.Error(err, "failed to apply ACL: "+err.Error())
			return err
		}
	}

	log.Info("acls applied")
	return nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) getACLPermissionTypeAndOperation(acl awsv1alpha1.AwsMSKDemoKafkaACL) (sarama.AclPermissionType, sarama.AclOperation) {

	permType := sarama.AclPermissionUnknown
	switch strings.ToLower(acl.PermissionType) {
	case "allow":
		permType = sarama.AclPermissionAllow
	case "deny":
		permType = sarama.AclPermissionDeny
	}

	op := sarama.AclOperationUnknown
	switch strings.ToLower(acl.Operation) {
	case "read":
		op = sarama.AclOperationRead
	case "write":
		op = sarama.AclOperationWrite
	case "all":
		op = sarama.AclOperationAll
	}

	return permType, op
}

func (r *AwsMSKDemoKafkaTopicReconciler) deleteMSKKafkaTopic(ctx context.Context, topic *awsv1alpha1.AwsMSKDemoKafkaTopic) error {
	log := log.FromContext(ctx)
	log.Info("trying to delete topic: " + topic.Spec.Name)

	if err := r.clusterAdmin.DeleteTopic(topic.Spec.Name); err != nil {
		log.Error(err, "failed to delete topic: "+err.Error())
		return err
	}

	log.Info("topic deleted: " + topic.Spec.Name)
	return nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) removeFinalizer(ctx context.Context, topic *awsv1alpha1.AwsMSKDemoKafkaTopic) error {
	log := log.FromContext(ctx)
	log.Info("trying to remove finalizer")

	if ok := controllerutil.RemoveFinalizer(topic, awsmskdemoinstanceFinalizer); !ok {
		err := fmt.Errorf("failed to remove finalizer from AwsMSKDemoKafkaTopic")
		log.Error(err, "failed to remove finalizer from AwsMSKDemoKafkaTopic")
		return err
	}
	if err := r.Update(ctx, topic); err != nil {
		log.Error(err, "failed to update AwsMSKDemoKafkaTopic to remove finalizer: "+err.Error())
		return err
	}

	log.Info("finalizer deleted")
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

func (r *AwsMSKDemoKafkaTopicReconciler) createTlsConfig(ctx context.Context) (*tls.Config, error) {
	// Load TLS certificates
	cert, err := r.loadCertificate(ctx)
	if err != nil {
		return nil, err
	}
	ca, err := r.loadRootCA(ctx)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            ca,
		InsecureSkipVerify: true,
	}, nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) loadCertificate(ctx context.Context) (tls.Certificate, error) {
	log := log.FromContext(ctx)
	log.Info("trying to load certificate")

	cert, err := tls.LoadX509KeyPair(sslCertLocation, sslKeyLocation)
	if err != nil {
		return tls.Certificate{}, err
	}

	log.Info("certificate loaded")
	return cert, nil
}

func (r *AwsMSKDemoKafkaTopicReconciler) loadRootCA(ctx context.Context) (*x509.CertPool, error) {
	log := log.FromContext(ctx)
	log.Info("trying to load root ca")

	caCert, err := os.ReadFile(sslCALocation)
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
