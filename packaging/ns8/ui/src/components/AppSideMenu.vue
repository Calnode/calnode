<!--
  Copyright (C) 2023 Nethesis S.r.l.
  SPDX-License-Identifier: GPL-3.0-or-later
-->
<template>
  <cv-side-nav id="app-side-nav" fixed expanded class="app-side-nav">
    <AppSideMenuContent />
  </cv-side-nav>
</template>

<script>
import AppSideMenuContent from "@/components/AppSideMenuContent";

export default {
  name: "AppSideMenu",
  components: { AppSideMenuContent },
};
</script>
