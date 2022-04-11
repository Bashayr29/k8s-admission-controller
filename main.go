package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	admission "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/kubernetes/pkg/apis/apps/v1"

	"encoding/json"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecFactory  = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecFactory.UniversalDeserializer()
)

// add kind AdmissionReview in scheme
func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admission.AddToScheme(runtimeScheme)
	_ = v1.AddToScheme(runtimeScheme)
}

type admitv1Func func(admission.AdmissionReview) *admission.AdmissionResponse

type admitHandler struct {
	v1 admitv1Func
}

func AdmitHandler(f admitv1Func) admitHandler {
	return admitHandler{
		v1: f,
	}
}

// serve handles the http portion of a request prior to handing to an admit
// function
func serve(w http.ResponseWriter, r *http.Request, admit admitHandler) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		log.Error().Msgf("contentType=%s, expect application/json", contentType)
		return
	}

	log.Info().Msgf("handling request: %s", body)
	var responseObj runtime.Object
	if obj, gvk, err := deserializer.Decode(body, nil, nil); err != nil {
		msg := fmt.Sprintf("Request could not be decoded: %v", err)
		log.Error().Msg(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return

	} else {
		requestedAdmissionReview, ok := obj.(*admission.AdmissionReview)
		if !ok {
			log.Error().Msgf("Expected v1.AdmissionReview but got: %T", obj)
			return
		}
		responseAdmissionReview := &admission.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = admit.v1(*requestedAdmissionReview)
		responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
		responseObj = responseAdmissionReview

	}
	log.Info().Msgf("sending response: %v", responseObj)
	respBytes, err := json.Marshal(responseObj)
	if err != nil {
		log.Err(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		log.Err(err)
	}
}

func serveMutate(w http.ResponseWriter, r *http.Request) {
	serve(w, r, AdmitHandler(mutate))
}
func serveValidate(w http.ResponseWriter, r *http.Request) {
	serve(w, r, AdmitHandler(validate))
}

// adds prefix 'prod' to every incoming Deployment, example: prod-apps
func mutate(ar admission.AdmissionReview) *admission.AdmissionResponse {
	log.Info().Msgf("mutating deployments")
	deploymentResource := metav1.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	if ar.Request.Resource != deploymentResource {
		log.Error().Msgf("expect resource to be %s", deploymentResource)
		return nil
	}
	raw := ar.Request.Object.Raw
	deployment := appsv1.Deployment{}

	if _, _, err := deserializer.Decode(raw, nil, &deployment); err != nil {
		log.Err(err)
		return &admission.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}
	newDeploymentName := fmt.Sprintf("prod-%s", deployment.GetName())
	pt := admission.PatchTypeJSONPatch
	deploymentPatch := fmt.Sprintf(`[{ "op": "add", "path": "/metadata/name", "value": "%s" }]`, newDeploymentName)
	return &admission.AdmissionResponse{Allowed: true, PatchType: &pt, Patch: []byte(deploymentPatch)}
}

// verify if a Deployment has the 'prod' prefix name
func validate(ar admission.AdmissionReview) *admission.AdmissionResponse {
	log.Info().Msgf("validating deployments")
	deploymentResource := metav1.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	if ar.Request.Resource != deploymentResource {
		log.Error().Msgf("expect resource to be %s", deploymentResource)
		return nil
	}
	raw := ar.Request.Object.Raw
	deployment := appsv1.Deployment{}
	if _, _, err := deserializer.Decode(raw, nil, &deployment); err != nil {
		log.Err(err)
		return &admission.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}
	if !strings.HasPrefix(deployment.GetName(), "prod-") {
		return &admission.AdmissionResponse{
			Allowed: false, Result: &metav1.Status{
				Message: "Deployment's prefix name \"prod\" not found",
			},
		}
	}
	return &admission.AdmissionResponse{Allowed: true}
}

func main() {
	var tlsKey, tlsCert string
	flag.StringVar(&tlsKey, "tlsKey", "/etc/certs/tls.key", "Path to the TLS key")
	flag.StringVar(&tlsCert, "tlsCert", "/etc/certs/tls.crt", "Path to the TLS certificate")
	flag.Parse()
	http.HandleFunc("/mutate", serveMutate)
	http.HandleFunc("/validate", serveValidate)
	log.Info().Msg("Server started ...")
	log.Fatal().Err(http.ListenAndServeTLS(":8443", tlsCert, tlsKey, nil)).Msg("webhook server exited")
}
