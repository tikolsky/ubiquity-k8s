package hookexecutor

import (
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

type preDeleteExecutor struct {
	*baseExcutor
}

func newPreDeleteExecutor(
	kubeClient kubernetes.Interface,
) *preDeleteExecutor {
	return &preDeleteExecutor{
		baseExcutor: &baseExcutor{
			kubeClient: kubeClient,
		},
	}
}

func (e *preDeleteExecutor) Execute() error {
	logger.Info("Performing actions in pre-delete")
	var err error

	err = e.DeleteUbiquityDBPods()
	if err != nil {
		return logger.ErrorRet(err, "Failed performing actions in pre-delete")
	}

	err = e.DeleteUbiquityDBPvc()
	if err != nil {
		return logger.ErrorRet(err, "Failed performing actions in pre-delete")
	}

	logger.Info("Successfully performed actions in pre-delete")
	return nil
}

// DeleteUbiquityDBPods sets replicas of ubiquity-db deployment to 0 and
// wait for all the relevant pods to be deleted.
func (e *preDeleteExecutor) DeleteUbiquityDBPods() error {
	ns := getCurrentNamespace()
	logger.Info(fmt.Sprintf("Deleting Pods under Ubiquity DB Deployment %s in namespace %s", ubiquityDBDeploymentName, ns))

	deploy, err := e.kubeClient.AppsV1().Deployments(ns).Get(ubiquityDBDeploymentName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("The Ubiquity DB Deployment is already deleted")
			return nil
		}
		return logger.ErrorRet(err, "Failed deleting Ubiquity DB Pods")
	}

	var zero = int32(0)
	deploy.Spec.Replicas = &zero
	_, err = e.kubeClient.AppsV1().Deployments(ns).Update(deploy)
	if err != nil {
		return logger.ErrorRet(err, "Failed deleting Ubiquity DB Pods")
	}
	if watchers, err := generatePodsWatchersInDeployment(deploy, e.kubeClient.CoreV1()); err != nil {
		return logger.ErrorRet(err, "Failed generating Pod watcher")
	} else {
		var wg sync.WaitGroup
		var watcherErr error

		for _, w := range watchers {
			wg.Add(1)
			go func() {
				_, err := Watch(w, nil, 40*time.Second)
				if err != nil {
					if watcherErr == nil {
						watcherErr = err
					}
				}
				wg.Done()
			}()
		}

		wg.Wait()

		if watcherErr != nil {
			return logger.ErrorRet(err, "Failed waiting Pod to be deleted")
		} else {
			logger.Info(fmt.Sprintf("Successfully Deleted Pods under Ubiquity DB Deployment %s", ubiquityDBDeploymentName))
			return nil
		}
	}
}

// DeleteUbiquityDBPvc deletes the ubiquity-db pvc and wait for pvc/pv to be deleted.
func (e *preDeleteExecutor) DeleteUbiquityDBPvc() error {
	ns := getCurrentNamespace()
	logger.Info(fmt.Sprintf("Deleting PVC %s in namespace %s", ubiquityDBPvcName, ns))

	pvc, err := e.kubeClient.CoreV1().PersistentVolumeClaims(ns).Get(ubiquityDBPvcName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info(fmt.Sprintf("The Ubiquity DB PVC %s is already deleted", ubiquityDBPvcName))
			return nil
		}
		return logger.ErrorRet(err, fmt.Sprintf("Failed deleting Ubiquity DB PVC %s", ubiquityDBPvcName))
	}

	pvcWatcher, err := generatePvcWatcher(pvc.Name, ns, e.kubeClient.CoreV1())
	if err != nil {
		return logger.ErrorRet(err, "Failed generating PVC watcher")
	}
	pvWatcher, err := generatePvWatcher(pvc.Spec.VolumeName, e.kubeClient.CoreV1())
	if err != nil {
		return logger.ErrorRet(err, "Failed generating PV watcher")
	}

	var wg sync.WaitGroup
	var watcherErr error

	for _, w := range []watch.Interface{pvcWatcher, pvWatcher} {
		wg.Add(1)
		go func() {
			_, err := Watch(w, nil)
			if err != nil {
				if watcherErr == nil {
					watcherErr = err
				}
			}
			wg.Done()
		}()
	}

	// start the watcher first and then delete the resource
	if err := e.kubeClient.CoreV1().PersistentVolumeClaims(ns).Delete(ubiquityDBPvcName, nil); err != nil {
		return logger.ErrorRet(err, fmt.Sprintf("Failed deleting Ubiquity DB PVC %s", ubiquityDBPvcName))
	}

	wg.Wait()

	if watcherErr != nil {
		return logger.ErrorRet(err, "Failed waiting Pod to be deleted")
	} else {
		logger.Info(fmt.Sprintf("Successfully Deleted PVC %s", ubiquityDBPvcName))
		return nil
	}

}
