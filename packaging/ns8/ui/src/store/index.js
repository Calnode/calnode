//
// Copyright (C) 2023 Nethesis S.r.l.
// SPDX-License-Identifier: GPL-3.0-or-later
//
import Vue from "vue";
import Vuex from "vuex";

Vue.use(Vuex);

export default new Vuex.Store({
  state: {
    appName: "",
    instanceName: "",
    instanceLabel: "",
    core: null,
  },
  mutations: {
    setInstanceName(state, instanceName) {
      state.instanceName = instanceName;
    },
    setInstanceLabel(state, instanceLabel) {
      state.instanceLabel = instanceLabel;
    },
    setCore(state, core) {
      state.core = core;
    },
    setAppName(state, appName) {
      state.appName = appName;
    },
  },
  actions: {
    setInstanceNameInStore(context, instanceName) {
      context.commit("setInstanceName", instanceName);
    },
    setInstanceLabelInStore(context, instanceLabel) {
      context.commit("setInstanceLabel", instanceLabel);
    },
    setCoreInStore(context, core) {
      context.commit("setCore", core);
    },
    setAppNameInStore(context, appName) {
      context.commit("setAppName", appName);
    },
  },
  modules: {},
});
