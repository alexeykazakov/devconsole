package component

import (
	"context"
	appsv1 "github.com/openshift/api/apps/v1"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	compv1alpha1 "github.com/redhat-developer/devconsole-operator/pkg/apis/devconsole/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
)

const (
	Name      = "MyComp"
	Namespace = "test-project"
)

// TestComponentController runs Component.Reconcile() against a
// fake client that tracks a Component object.
func TestComponentController(t *testing.T) {
	reqLogger := log.WithValues("Test", t.Name())
	reqLogger.Info("TestComponentController")

	// A Component resource with metadata and spec.
	cp := &compv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: Namespace,
		},
		Spec: compv1alpha1.ComponentSpec{
			BuildType: "nodejs",
			Codebase:  "https://somegit.con/myrepo",
		},
	}

	// Register operator types with the runtime scheme.
	s := scheme.Scheme
	s.AddKnownTypes(compv1alpha1.SchemeGroupVersion, cp)

	// register openshift resource specific schema
	if err := imagev1.AddToScheme(s); err != nil {
		log.Error(err, "")
		assert.Nil(t, err, "adding imagestream schema is failing")
	}
	if err := buildv1.AddToScheme(s); err != nil {
		log.Error(err, "")
		assert.Nil(t, err, "adding buildconfig schema is failing")
	}
	if err := appsv1.AddToScheme(s); err != nil {
		log.Error(err, "")
		assert.Nil(t, err, "adding deploymentconfig, apps schema is failing")
	}

	t.Run("with ReconcileComponent CR containing all required field creates openshift resources", func(t *testing.T) {
		//given
		// Objects to track in the fake client.
		objs := []runtime.Object{
			cp,
		}
		// Create a fake client to mock API calls.
		cl := fake.NewFakeClient(objs...)

		// Create a ReconcileComponent object with the scheme and fake client.
		r := &ReconcileComponent{client: cl, scheme: s}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      Name,
				Namespace: Namespace,
			},
		}

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err, "reconcile is failing")

		instance := &compv1alpha1.Component{}
		errGet := r.client.Get(context.TODO(), req.NamespacedName, instance)
		require.NoError(t, errGet, "component is not created")

		is := &imagev1.ImageStream{}
		errGetImage := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, is)
		require.NoError(t, errGetImage, "output imagestream is not created")

		isBuilder := &imagev1.ImageStream{}
		errGetBuilderImage := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: cp.Spec.BuildType}, isBuilder)
		require.NoError(t, errGetBuilderImage, "builder imagestream is not created")
		require.Equal(t, cp.Spec.BuildType, isBuilder.ObjectMeta.Name, "imagestream builder shoul be named after component's buildtype")
		require.Equal(t, Namespace, isBuilder.ObjectMeta.Namespace, "")
		require.Equal(t, 1, len(isBuilder.Labels), "imagestream builder should contain one label")
		require.Equal(t, Name, isBuilder.Labels["app"], "imagestream builder should have one label with name of CR.")
		require.Equal(t, 1, len(isBuilder.Spec.Tags), "imagestream builder should have a tag specified when")
		require.Equal(t, "latest", isBuilder.Spec.Tags[0].Name, "imagestream builder should take latest version")
		require.Equal(t, "DockerImage", isBuilder.Spec.Tags[0].From.Kind, "imagestream builder should be taken from docker when not found in cluster")
		require.Equal(t, "nodeshift/centos7-s2i-nodejs:10.x", isBuilder.Spec.Tags[0].From.Name, "imagestream builder should be taken from nodeshift/centos7-s2i-nodejs:10.x")

		bc := &buildv1.BuildConfig{}
		errGetBC := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, bc)
		require.NoError(t, errGetBC, "build config is not created")
		require.Equal(t, "https://somegit.con/myrepo", bc.Spec.Source.Git.URI, "build config should not have any source attached")
		require.Equal(t, 2, len(bc.Spec.Triggers), "build config contains 2 triggers")
		require.Equal(t, buildv1.ConfigChangeBuildTriggerType, bc.Spec.Triggers[0].Type, "build config should be triggered on config change")
		require.Equal(t, buildv1.ImageChangeBuildTriggerType, bc.Spec.Triggers[1].Type, "build config should be triggered on image change")
		require.Equal(t, 1, len(bc.Labels), "bc should contain one label")
		require.Equal(t, Name, bc.ObjectMeta.Labels["app"], "bc builder should have one label with name of CR.")

		dc := &appsv1.DeploymentConfig{}
		errGetDC := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, dc)
		require.NoError(t, errGetDC, "deployment config is not created")
		require.Equal(t, 2, len(dc.Spec.Triggers), "deployment config contains 2 triggers")
		require.Equal(t, appsv1.DeploymentTriggerOnConfigChange, dc.Spec.Triggers[0].Type, "deployment config should be triggered by DeploymentTriggerOnConfigChange")
		require.Equal(t, appsv1.DeploymentTriggerOnImageChange, dc.Spec.Triggers[1].Type, "deployment config should be triggered by DeploymentTriggerOnImageChange")
		require.Equal(t, Name+":latest", dc.Spec.Triggers[1].ImageChangeParams.From.Name, "deployment config should be triggered by DeploymentTriggerOnImageChange from bc-output")
		require.Equal(t, 1, len(dc.Labels), "dc should contain one label")
		require.Equal(t, Name, dc.ObjectMeta.Labels["app"], "dc should have one label with name of CR.")
	})

	t.Run("with ReconcileComponent CR containing all required field and buildtype matches openshift namespace imagestream", func(t *testing.T) {
		//given
		isNodejs := &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nodejs",
				Namespace: "openshift",
			},
			Spec: imagev1.ImageStreamSpec{},
		}
		// Objects to track in the fake client.
		objs := []runtime.Object{
			cp,
			isNodejs,
		}
		// Create a fake client to mock API calls.
		cl := fake.NewFakeClient(objs...)

		// Create a ReconcileComponent object with the scheme and fake client.
		r := &ReconcileComponent{client: cl, scheme: s}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      Name,
				Namespace: Namespace,
			},
		}

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err, "reconcile is failing")

		instance := &compv1alpha1.Component{}
		errGet := r.client.Get(context.TODO(), req.NamespacedName, instance)
		require.NoError(t, errGet, "component is not created")

		is := &imagev1.ImageStream{}
		errGetImage := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, is)
		require.NoError(t, errGetImage, "output imagestream is not created")

		isBuilder := &imagev1.ImageStream{}
		errGetBuilderImage := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: cp.Spec.BuildType}, isBuilder)
		require.Error(t, errGetBuilderImage, "builder imagestream should not be created")

		bc := &buildv1.BuildConfig{}
		errGetBC := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, bc)
		require.NoError(t, errGetBC, "build config is not created")
		require.Equal(t, "https://somegit.con/myrepo", bc.Spec.Source.Git.URI, "build config should not have any source attached")
		require.Equal(t, 2, len(bc.Spec.Triggers), "build config contains 2 triggers")
		require.Equal(t, buildv1.ConfigChangeBuildTriggerType, bc.Spec.Triggers[0].Type, "build config should be triggered on config change")
		require.Equal(t, buildv1.ImageChangeBuildTriggerType, bc.Spec.Triggers[1].Type, "build config should be triggered on image change")
		require.Equal(t, "openshift", bc.Spec.CommonSpec.Strategy.SourceStrategy.From.Namespace, "builder image used in build config should be taken from openshift namespace")
		require.Equal(t, "nodejs:latest", bc.Spec.CommonSpec.Strategy.SourceStrategy.From.Name, "builder image used in build config should be taken from openshift's nodejs image")
		require.Equal(t, 1, len(bc.Labels), "bc should contain one label")
		require.Equal(t, Name, bc.ObjectMeta.Labels["app"], "bc builder should have one label with name of CR.")

		dc := &appsv1.DeploymentConfig{}
		errGetDC := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, dc)
		require.NoError(t, errGetDC, "deployment config is not created")
		require.Equal(t, 2, len(dc.Spec.Triggers), "deployment config contains 2 triggers")
		require.Equal(t, appsv1.DeploymentTriggerOnConfigChange, dc.Spec.Triggers[0].Type, "deployment config should be triggered by DeploymentTriggerOnConfigChange")
		require.Equal(t, appsv1.DeploymentTriggerOnImageChange, dc.Spec.Triggers[1].Type, "deployment config should be triggered by DeploymentTriggerOnImageChange")
		require.Equal(t, Name+":latest", dc.Spec.Triggers[1].ImageChangeParams.From.Name, "deployment config should be triggered by DeploymentTriggerOnImageChange from bc-output")
		require.Equal(t, 1, len(dc.Labels), "dc should contain one label")
		require.Equal(t, Name, dc.ObjectMeta.Labels["app"], "dc should have one label with name of CR.")
	})

	t.Run("with ReconcileComponent CR without buildtype", func(t *testing.T) {
		//given
		objs := []runtime.Object{
			cp,
		}
		cp.Spec.BuildType = ""
		cp.Spec.Codebase = "https://somegit.con/myrepo"
		// Create a fake client to mock API calls.
		cl := fake.NewFakeClient(objs...)

		// Create a ReconcileComponent object with the scheme and fake client.
		r := &ReconcileComponent{client: cl, scheme: s}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      Name,
				Namespace: Namespace,
			},
		}

		//when
		_, err := r.Reconcile(req)

		//then
		require.Error(t, err, "reconcile is failing")

		instance := &compv1alpha1.Component{}
		errGet := r.client.Get(context.TODO(), req.NamespacedName, instance)
		require.NoError(t, errGet, "component is not created")

		is := &imagev1.ImageStream{}
		errGetImage := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, is)
		require.NoError(t, errGetImage, "output imagestream is not created")

		bc := &buildv1.BuildConfig{}
		errGetBC := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, bc)
		require.Error(t, errGetBC, "build config should not not created with missing CR's buildtype")
		require.Equal(t, errors.ReasonForError(errGetBC), metav1.StatusReasonNotFound, "bc could not found associated imagestream")

		dc := &appsv1.DeploymentConfig{}
		errGetDC := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, dc)
		require.Error(t, errGetDC, "deployment config should not be created")
	})

	t.Run("with ReconcileComponent CR without codebases", func(t *testing.T) {
		//given
		objs := []runtime.Object{
			cp,
		}
		cp.Spec.BuildType = "nodejs"
		cp.Spec.Codebase = ""
		// Create a fake client to mock API calls.
		cl := fake.NewFakeClient(objs...)

		// Create a ReconcileComponent object with the scheme and fake client.
		r := &ReconcileComponent{client: cl, scheme: s}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      Name,
				Namespace: Namespace,
			},
		}

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err, "reconcile is failing")

		instance := &compv1alpha1.Component{}
		errGet := r.client.Get(context.TODO(), req.NamespacedName, instance)
		require.NoError(t, errGet, "component is not created")

		is := &imagev1.ImageStream{}
		errGetImage := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, is)
		require.NoError(t, errGetImage, "output imagestream is not created")

		isBuilder := &imagev1.ImageStream{}
		errGetBuilderImage := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, isBuilder)
		require.NoError(t, errGetBuilderImage, "builder imagestream is not created")

		bc := &buildv1.BuildConfig{}
		errGetBC := cl.Get(context.Background(), types.NamespacedName{Namespace: Namespace, Name: Name}, bc)
		require.NoError(t, errGetBC, "buildconfig is not created")
		require.Equal(t, "", bc.Spec.Source.Git.URI, "build config should not have any source attached")
	})
}
