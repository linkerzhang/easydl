// Copyright 2022 The EasyDL Authors. All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package psstrategy

import (
	"context"
	"sort"
	"strconv"

	elasticv1alpha1 "github.com/intelligent-machine-learning/easydl/operator/api/v1alpha1"
	common "github.com/intelligent-machine-learning/easydl/operator/pkg/common"
	logger "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	runtime_client "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	workerServicePort int = 2222
)

// WorkerManager generates a master pod object.
type WorkerManager struct {
	PSTaskManager
}

func init() {
	logger.Infof("init worker manager")
	common.ReplicaManagers[ReplicaTypeWorker] = newWorkerManager()
}

func newWorkerManager() *WorkerManager {
	return &WorkerManager{
		PSTaskManager: PSTaskManager{
			taskType: ReplicaTypeWorker,
		},
	}
}

// ReconcilePods creates a Pod on a K8s cluster
func (m *WorkerManager) ReconcilePods(
	client runtime_client.Client,
	job *elasticv1alpha1.ElasticJob,
	resourceSpec *elasticv1alpha1.ReplicaResourceSpec,
) error {
	workerStatus := m.getTaskStatus(job)
	currentNum := m.getTotalTaskCount(workerStatus)
	aliveNum := int(workerStatus.Active + workerStatus.Pending)
	if resourceSpec.Replicas > aliveNum {
		err := m.scaleUpWorkers(client, job, currentNum, resourceSpec.Replicas-aliveNum)
		if err != nil {
			return err
		}
	} else {
		err := m.scaleDownWorkers(client, job, aliveNum-resourceSpec.Replicas)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *WorkerManager) scaleUpWorkers(
	client runtime_client.Client,
	job *elasticv1alpha1.ElasticJob,
	currentNum int,
	upNum int,
) error {
	cluster := m.getPSCluster(client, job)
	if cluster.Worker == nil {
		cluster.Worker = make(map[int]string)
	}
	for i := currentNum; i < currentNum+upNum; i++ {
		cluster.Worker[i] = m.newTaskServiceAddr(job.Name, i, workerServicePort)
		worker := m.newTask(job, i)
		m.insertTfConfigToEnv(&worker.Spec.Containers[0], cluster, i)
		err := client.Create(context.Background(), worker)
		if err != nil {
			return err
		}
		service := m.newTaskService(job, i, workerServicePort)
		err = client.Create(context.Background(), service)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *WorkerManager) scaleDownWorkers(
	client runtime_client.Client,
	job *elasticv1alpha1.ElasticJob,
	downNum int,
) error {
	workers, err := common.GetReplicaTypePods(client, job, m.taskType)
	if errors.IsNotFound(err) {
		logger.Warningf("No any worker found: %v", err)
		return nil
	}
	aliveWorkers := make(map[int]*corev1.Pod)
	workerIndices := []int{}
	for _, worker := range workers {
		if worker.Status.Phase == corev1.PodRunning || worker.Status.Phase == corev1.PodPending {
			workerIndex, _ := strconv.Atoi(
				worker.Labels[common.LabelReplicaIndexKey],
			)
			workerIndices = append(workerIndices, workerIndex)
			aliveWorkers[workerIndex] = &worker
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(workerIndices)))
	for i := 0; i < downNum; i++ {
		common.DeletePod(client, aliveWorkers[i])
	}

	return nil
}
