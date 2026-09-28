<!--
  Copyright (C) 2023 Nethesis S.r.l.
  SPDX-License-Identifier: GPL-3.0-or-later
-->
<template>
  <div id="ns8-app">
    <cv-content id="main-content" class="app-content">
      <AppSideMenu />
      <AppMobileSideMenu />
      <router-view />
    </cv-content>
  </div>
</template>

<script>
import AppSideMenu from "./components/AppSideMenu";
import AppMobileSideMenu from "./components/AppMobileSideMenu";
import { mapState, mapActions } from "vuex";
import {
  QueryParamService,
  TaskService,
  UtilService,
} from "@nethserver/ns8-ui-lib";
import to from "await-to-js";

export default {
  name: "App",
  components: { AppSideMenu, AppMobileSideMenu },
  mixins: [QueryParamService, TaskService, UtilService],
  computed: {
    ...mapState(["instanceName", "instanceLabel", "core"]),
  },
  created() {
    const core = window.parent.core;
    this.setCoreInStore(core);
    const instanceName = /#\/apps\/([a-zA-Z0-9_-]+)/.exec(
      window.parent.location.hash
    )[1];
    this.setInstanceNameInStore(instanceName);
    this.getInstanceLabel();
    this.setAppName();

    // listen to change route events
    const context = this;
    window.addEventListener(
      "changeRoute",
      function (e) {
        const requestedPage = e.detail;
        context.$router.replace(requestedPage);
      },
      false
    );

    // configure global shortcuts
    core.$root.$emit("configureKeyboardShortcuts", window);

    const queryParams = this.getQueryParamsForApp();
    const requestedPage = queryParams.page || "status";

    if (requestedPage != "status") {
      this.$router.replace(requestedPage);
    }
  },
  methods: {
    ...mapActions([
      "setInstanceNameInStore",
      "setInstanceLabelInStore",
      "setCoreInStore",
      "setAppNameInStore",
    ]),
    async getInstanceLabel() {
      const taskAction = "get-name";
      const eventId = this.getUuid();

      // register to task error
      this.core.$root.$once(
        `${taskAction}-aborted-${eventId}`,
        this.getInstanceLabelAborted
      );

      // register to task completion
      this.core.$root.$once(
        `${taskAction}-completed-${eventId}`,
        this.getInstanceLabelCompleted
      );

      const res = await to(
        this.createModuleTaskForApp(this.instanceName, {
          action: taskAction,
          extra: {
            title: this.$t("action." + taskAction),
            isNotificationHidden: true,
            eventId,
          },
        })
      );
      const err = res[0];

      if (err) {
        console.error(`error creating task ${taskAction}`, err);
        this.createErrorNotificationForApp(
          err,
          this.$t("task.cannot_create_task", { action: taskAction })
        );
        return;
      }
    },
    getInstanceLabelAborted(taskResult, taskContext) {
      console.error(`${taskContext.action} aborted`, taskResult);
    },
    getInstanceLabelCompleted(taskContext, taskResult) {
      this.setInstanceLabelInStore(taskResult.output.name);
    },
    setAppName() {
      const metadata = require("../public/metadata.json");
      const appName = metadata.name;
      this.setAppNameInStore(appName);
    },
  },
};
</script>

<style lang="scss">
@import "styles/carbon-utils";
</style>
