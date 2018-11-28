package hookexecutor

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	_ "github.com/IBM/ubiquity-k8s/cmd/hook-executor/logger"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	fakekubeclientset "k8s.io/client-go/kubernetes/fake"
	testcore "k8s.io/client-go/testing"
)

var _ = Describe("Sanity", func() {

	var e Executor
	var kubeClient *fakekubeclientset.Clientset
	var pvc *v1.PersistentVolumeClaim
	var pod *v1.Pod
	var testNamespace = "test"
	var stopped chan bool

	BeforeEach(func() {

		stopped = make(chan bool)

		os.Setenv("STORAGE_CLASS", "gold")
		os.Setenv("NAMESPACE", testNamespace)

		pvc, pod = getSanityPvcAndPod()

		kubeClient = fakekubeclientset.NewSimpleClientset()

		e = SanityExecutor(kubeClient)
	})

	AfterEach(func() {
		os.Setenv("STORAGE_CLASS", "")
		os.Setenv("NAMESPACE", "")
		close(stopped)
	})

	Describe("test Execute", func() {

		Context("update namespace", func() {

			BeforeEach(func() {
				os.Setenv("NAMESPACE", testNamespace)

				Expect(pvc.Namespace).NotTo(Equal(testNamespace))
				Expect(pod.Namespace).NotTo(Equal(testNamespace))

				err := updateNamespace([]runtime.Object{pvc, pod})
				Ω(err).ShouldNot(HaveOccurred())

			})

			It("should update the namespace to the specified one", func() {
				Expect(pvc.Namespace).To(Equal(testNamespace))
				Expect(pod.Namespace).To(Equal(testNamespace))
			})
		})

		Context("create sanity resources", func() {

			BeforeEach(func() {
				pvc.SetNamespace(testNamespace)
				pod.SetNamespace(testNamespace)

				// pvc and pod are not existing on API Server at first.
				_, err := kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(pvc.Name, metav1.GetOptions{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				_, err = kubeClient.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				go func() {
					pvcWatcher := watch.NewFake()
					kubeClient.PrependWatchReactor("persistentvolumeclaims", testcore.DefaultWatchReactor(pvcWatcher, nil))

					// sleep and set the phase of the pvc to "Bound"
					time.Sleep(50 * time.Millisecond)
					newPvc := pvc.DeepCopy()
					newPvc.Status.Phase = v1.ClaimBound
					pvcWatcher.Modify(newPvc)
				}()

				go func() {
					podWatcher := watch.NewFake()
					kubeClient.PrependWatchReactor("pods", testcore.DefaultWatchReactor(podWatcher, nil))

					// sleep and set the phase of the pvc to "Running", should sleep longer that pvc
					time.Sleep(100 * time.Millisecond)
					newPod := pod.DeepCopy()
					newPod.Status.Phase = v1.PodRunning
					podWatcher.Modify(newPod)
				}()

				go func() {
					err := e.(*sanityExecutor).createSanityResources()
					Ω(err).ShouldNot(HaveOccurred())
					stopped <- true
				}()
			})

			It("should create pod and pvc successfully", func(done Done) {
				Expect(<-stopped).To(BeTrue())

				_, err := kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(pvc.Name, metav1.GetOptions{})
				Ω(err).ShouldNot(HaveOccurred())

				_, err = kubeClient.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
				Ω(err).ShouldNot(HaveOccurred())

				close(done)
			})
		})

		Context("revert changes if creating sanity resources is failed", func() {

			BeforeEach(func() {
				pvc.SetNamespace(testNamespace)
				pod.SetNamespace(testNamespace)

				go func() {
					pvcWatcher := watch.NewFake()
					kubeClient.PrependWatchReactor("persistentvolumeclaims", testcore.DefaultWatchReactor(pvcWatcher, nil))

					// sleep and set the phase of the pvc to "Bound"
					time.Sleep(50 * time.Millisecond)
					newPvc := pvc.DeepCopy()
					newPvc.Status.Phase = v1.ClaimBound
					pvcWatcher.Modify(newPvc)
				}()
				// create the pod in advance to generate a "pod already exists" error
				kubeClient.CoreV1().Pods(pod.Namespace).Create(pod)

				go func() {
					err := e.(*sanityExecutor).createSanityResources()
					Ω(err).Should(HaveOccurred())
					Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
					stopped <- true
				}()
			})

			It("should delete the pvc and pod before exit", func(done Done) {
				Expect(<-stopped).To(BeTrue())

				_, err := kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(pvc.Name, metav1.GetOptions{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				_, err = kubeClient.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				close(done)
			})
		})

		Context("delete sanity resources", func() {

			BeforeEach(func() {
				pvc.SetNamespace(testNamespace)
				pod.SetNamespace(testNamespace)

				// create the pod and pvc
				_, err := kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Create(pvc)
				Ω(err).ShouldNot(HaveOccurred())
				_, err = kubeClient.CoreV1().Pods(pod.Namespace).Create(pod)
				Ω(err).ShouldNot(HaveOccurred())

				// check pod and pvc are existing on API Server
				_, err = kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(pvc.Name, metav1.GetOptions{})
				Ω(err).ShouldNot(HaveOccurred())
				_, err = kubeClient.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
				Ω(err).ShouldNot(HaveOccurred())

				go func() {
					pvcWatcher := watch.NewFake()
					kubeClient.PrependWatchReactor("persistentvolumeclaims", testcore.DefaultWatchReactor(pvcWatcher, nil))

					// should sleep longer than pod
					time.Sleep(100 * time.Millisecond)
					pvcWatcher.Delete(pvc)
				}()

				go func() {
					podWatcher := watch.NewFake()
					kubeClient.PrependWatchReactor("pods", testcore.DefaultWatchReactor(podWatcher, nil))

					time.Sleep(50 * time.Millisecond)
					podWatcher.Delete(pod)
				}()

				go func() {
					err := e.(*sanityExecutor).deleteSanityResources()
					Ω(err).ShouldNot(HaveOccurred())
					stopped <- true
				}()
			})

			It("should delete pod and pvc successfully", func(done Done) {
				Expect(<-stopped).To(BeTrue())

				_, err := kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(pvc.Name, metav1.GetOptions{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				_, err = kubeClient.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				close(done)
			})
		})
	})
})
