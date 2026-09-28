<!--
  Copyright (C) 2023 Nethesis S.r.l.
  SPDX-License-Identifier: GPL-3.0-or-later
-->
<template>
  <cv-grid fullWidth>
    <cv-row>
      <cv-column class="page-title">
        <h2>{{ $t("about.title") }}</h2>
      </cv-column>
    </cv-row>
    <cv-row>
      <cv-column>
        <NsInlineNotification
          v-if="error.version"
          kind="error"
          :title="$t('error.cannot_retrieve_installed_modules')"
          :description="error.version"
          :showCloseButton="false"
        />
      </cv-column>
    </cv-row>
    <cv-row>
      <cv-column>
        <cv-tile :light="true">
          <cv-skeleton-text
            v-if="loading.moduleInfo"
            :paragraph="true"
            :line-count="12"
            width="80%"
          ></cv-skeleton-text>
          <div v-else-if="error.moduleInfo">
            <NsInlineNotification
              kind="error"
              :title="$t('error.cannot_retrieve_module_info')"
              :description="error.moduleInfo"
              :showCloseButton="false"
            />
          </div>
          <div v-else>
            <div class="logo-and-name">
              <div class="app-logo">
                <img
                  :src="
                    app.logo
                      ? app.logo
                      : require('@/assets/module_default_logo.png')
                  "
                  :alt="app.name + ' logo'"
                />
              </div>
              <div class="app-name">
                <h3>{{ app.name }}</h3>
              </div>
            </div>
            <div class="description">
              {{ getApplicationDescription(app) }}
            </div>
            <div class="key-value-setting">
              <span class="label">{{
                core.$t("software_center.instance")
              }}</span>
              <span class="value">{{ instanceName }}</span>
            </div>
            <div class="key-value-setting">
              <span class="label">{{ core.$t("common.version") }}</span>
              <span class="value">
                <cv-skeleton-text
                  v-if="loading.version"
                  class="version-skeleton"
                ></cv-skeleton-text>
                <span v-else>
                  {{ version }}
                </span>
              </span>
            </div>
            <div class="key-value-setting">
              <span class="label">{{
                core.$tc("software_center.categories", app.categories.length)
              }}</span>
              <span class="value">{{ getApplicationCategories(app) }}</span>
            </div>
            <div class="key-value-setting">
              <span class="label">{{
                core.$t("software_center.documentation")
              }}</span>
              <span class="value">
                <cv-link :href="app.docs.documentation_url" target="_blank">
                  {{ app.docs.documentation_url }}
                </cv-link>
              </span>
            </div>
            <div class="key-value-setting">
              <span class="label">{{ core.$t("software_center.bugs") }}</span>
              <span class="value">
                <cv-link :href="app.docs.bug_url" target="_blank">
                  {{ app.docs.bug_url }}
                </cv-link>
              </span>
            </div>
            <div class="key-value-setting">
              <span class="label">{{
                core.$t("software_center.source_code")
              }}</span>
              <span class="value">
                <cv-link :href="app.docs.code_url" target="_blank">
                  {{ app.docs.code_url }}
                </cv-link>
              </span>
            </div>
            <div class="key-value-setting">
              <span class="label">{{
                core.$t("software_center.source_package")
              }}</span>
              <span class="value">
                {{ app.source }}
              </span>
            </div>
            <div class="key-value-setting">
              <span class="label">{{
                core.$tc("software_center.authors", app.authors.length)
              }}</span>
              <span class="value">
                <span v-if="app.authors.length == 1"
                  >{{ app.authors[0].name }}
                  <cv-link
                    v-if="app.authors[0].email"
                    :href="'mailto:' + app.authors[0].email"
                    target="_blank"
                    class="email"
                  >
                    {{ app.authors[0].email }}
                  </cv-link>
                </span>
                <ul v-else class="authors">
                  <li
                    v-for="(author, index) in app.authors"
                    :key="index"
                    class="author"
                  >
                    {{ author.name }}
                    <cv-link
                      v-if="author.email"
                      :href="'mailto:' + author.email"
                      target="_blank"
                      class="email"
                    >
                      {{ author.email }}
                    </cv-link>
                  </li>
                </ul>
              </span>
            </div>
          </div>
        </cv-tile>
      </cv-column>
    </cv-row>
  </cv-grid>
</template>

<script>
import to from "await-to-js";
import { mapState } from "vuex";
import {
  QueryParamService,
  TaskService,
  UtilService,
  PageTitleService,
} from "@nethserver/ns8-ui-lib";

export default {
  name: "About",
  components: {},
  mixins: [TaskService, QueryParamService, UtilService, PageTitleService],
  pageTitle() {
    return this.$t("about.title") + " - " + this.appName;
  },
  data() {
    return {
      q: {
        page: "about",
      },
      urlCheckInterval: null,
      app: null,
      version: "",
      error: {
        moduleInfo: "",
        version: "",
      },
      loading: {
        moduleInfo: true,
        version: true,
      },
    };
  },
  computed: {
    ...mapState(["core", "appName", "instanceName"]),
  },
  created() {
    this.getModuleInfo();

    // needed to retrieve module version
    this.listInstalledModules();
  },
  beforeRouteEnter(to, from, next) {
    next((vm) => {
      vm.watchQueryData(vm);
      vm.urlCheckInterval = vm.initUrlBindingForApp(vm, vm.q.page);
    });
  },
  beforeRouteLeave(to, from, next) {
    clearInterval(this.urlCheckInterval);
    next();
  },
  methods: {
    getModuleInfo() {
      this.loading.moduleInfo = true;
      const metadata = require("../../public/metadata.json");
      this.app = metadata;
      this.loading.moduleInfo = false;
    },
    getApplicationDescription(app) {
      return this.getAppDescription(app, this.core);
    },
    getApplicationCategories(app) {
      return this.getAppCategories(app, this.core) || "-";
    },
    async listInstalledModules() {
      const taskAction = "list-installed-modules";
      const eventId = this.getUuid();

      // register to task error
      this.core.$root.$once(
        `${taskAction}-aborted-${eventId}`,
        this.listInstalledModulesAborted
      );

      // register to task completion
      this.core.$root.$once(
        `${taskAction}-completed-${eventId}`,
        this.listInstalledModulesCompleted
      );

      const res = await to(
        this.createClusterTaskForApp({
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
        this.error.version = this.getErrorMessage(err);
        this.loading.version = false;
        return;
      }
    },
    listInstalledModulesAborted(taskResult, taskContext) {
      console.error(`${taskContext.action} aborted`, taskResult);
      this.error.version = this.$t("error.generic_error");
      this.loading.version = false;
    },
    listInstalledModulesCompleted(taskContext, taskResult) {
      let apps = [];

      for (let instanceList of Object.values(taskResult.output)) {
        for (let instance of instanceList) {
          apps.push(instance);
        }
      }
      const app = apps.find((el) => el.id == this.instanceName);
      this.version = app.version;
      this.loading.version = false;
    },
  },
};
</script>

<style scoped lang="scss">
@import "../styles/carbon-utils";

.logo-and-name {
  display: flex;
  align-items: center;
  margin-top: $spacing-05;
  margin-bottom: $spacing-07;
}

.app-logo {
  max-width: 4rem;
  max-height: 4rem;
  margin-right: $spacing-05;
  flex-shrink: 0;
}

.app-logo img {
  width: 100%;
  height: 100%;
}

.description {
  margin-bottom: $spacing-07;
}

section {
  margin-bottom: $spacing-05;
}

.section-title {
  font-weight: bold;
}

.author {
  margin-left: $spacing-05;
  margin-bottom: $spacing-02;
}

.authors {
  margin-top: $spacing-02;
}

.email {
  margin-left: $spacing-02;
}

.version-skeleton {
  display: inline-flex;
  width: 3rem !important;
  position: relative;
  top: 1px;
}
</style>

<style lang="scss">
@import "../styles/carbon-utils";

// global styles

.version-skeleton .bx--skeleton__text {
  margin-bottom: 0;
  height: 12px;
}
</style>
