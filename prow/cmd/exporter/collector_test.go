/*
Copyright 2019 The Kubernetes Authors.

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

package main

import (
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

func TestKubeLabelsToPrometheusLabels(t *testing.T) {
	testcases := []struct {
		description         string
		labels              map[string]string
		expectedLabelKeys   []string
		expectedLabelValues []string
	}{
		{
			description:         "empty labels",
			labels:              map[string]string{},
			expectedLabelKeys:   []string{},
			expectedLabelValues: []string{},
		},
		{
			description: "labels with infra role",
			labels: map[string]string{
				"ci.openshift.io/role": "infra",
				"created-by-prow":      "true",
				"prow.k8s.io/build-id": "",
				"prow.k8s.io/id":       "35bca360-e085-11e9-8586-0a58ac104c36",
				"prow.k8s.io/job":      "periodic-prow-auto-config-brancher",
				"prow.k8s.io/type":     "periodic",
			},
			expectedLabelKeys: []string{
				"label_ci_openshift_io_role",
				"label_created_by_prow",
				"label_prow_k8s_io_build_id",
				"label_prow_k8s_io_id",
				"label_prow_k8s_io_job",
				"label_prow_k8s_io_type",
			},
			expectedLabelValues: []string{
				"infra",
				"true",
				"",
				"35bca360-e085-11e9-8586-0a58ac104c36",
				"periodic-prow-auto-config-brancher",
				"periodic",
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			actualLabelKeys, actualLabelValues := kubeLabelsToPrometheusLabels(tc.labels, "label_")
			assertEqual(t, actualLabelKeys, tc.expectedLabelKeys)
			assertEqual(t, actualLabelValues, tc.expectedLabelValues)
		})
	}
}

func assertEqual(t *testing.T, actual, expected interface{}) {
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("actual differs from expected:\n%s", diff.ObjectReflectDiff(expected, actual))
	}
}

func TestFilterWithBlacklist(t *testing.T) {
	testcases := []struct {
		description string
		labels      map[string]string
		expected    map[string]string
	}{
		{
			description: "nil labels",
			labels:      nil,
			expected:    nil,
		},
		{
			description: "empty labels",
			labels:      map[string]string{},
			expected:    map[string]string{},
		},
		{
			description: "normal labels",
			labels: map[string]string{
				"created-by-prow":       "true",
				"event-GUID":            "770bab40-e601-11e9-8e50-08c45d902b6f",
				"prow.k8s.io/refs.org":  "kubernetes",
				"prow.k8s.io/refs.pull": "14543",
				"prow.k8s.io/refs.repo": "test-infra",
				"prow.k8s.io/type":      "presubmit",
				"ci.openshift.io/role":  "infra",
			},
			expected: map[string]string{
				"event-GUID":           "770bab40-e601-11e9-8e50-08c45d902b6f",
				"ci.openshift.io/role": "infra",
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			actual := filterWithBlacklist(tc.labels)
			assertEqual(t, actual, tc.expected)
		})
	}
}

func TestGetLatest(t *testing.T) {
	time1 := time.Now()
	time2 := time1.Add(time.Minute)
	time3 := time2.Add(time.Minute)

	testcases := []struct {
		description string
		jobs        []*prowapi.ProwJob
		expected    map[string]*prowapi.ProwJob
	}{
		{
			description: "nil jobs",
			jobs:        nil,
			expected:    map[string]*prowapi.ProwJob{},
		},
		{
			description: "jobs with or without StartTime",
			jobs: []*prowapi.ProwJob{
				{
					Spec: prowapi.ProwJobSpec{
						Job: "job0",
					},
					Status: prowapi.ProwJobStatus{},
				},
				{
					Spec: prowapi.ProwJobSpec{
						Job: "job1",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time1}},
				},
				{
					Spec: prowapi.ProwJobSpec{
						Job: "job1",
					},
					Status: prowapi.ProwJobStatus{},
				},
				{
					Spec: prowapi.ProwJobSpec{
						Job: "job2",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time1}},
				},
				{
					Spec: prowapi.ProwJobSpec{
						Job: "job2",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time3}},
				},
				{
					Spec: prowapi.ProwJobSpec{
						Job: "job2",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time2}},
				},
				{
					Spec: prowapi.ProwJobSpec{
						Job: "job3",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time3}},
				},
			},
			expected: map[string]*prowapi.ProwJob{
				"job0": {
					Spec: prowapi.ProwJobSpec{
						Job: "job0",
					},
					Status: prowapi.ProwJobStatus{},
				},
				"job1": {
					Spec: prowapi.ProwJobSpec{
						Job: "job1",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time1}},
				},
				"job2": {
					Spec: prowapi.ProwJobSpec{
						Job: "job2",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time3}},
				},
				"job3": {
					Spec: prowapi.ProwJobSpec{
						Job: "job3",
					},
					Status: prowapi.ProwJobStatus{StartTime: metav1.Time{Time: time3}},
				},
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			actual := getLatest(tc.jobs)
			assertEqual(t, actual, tc.expected)
		})
	}
}
