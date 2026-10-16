module.exports = {
  publicPath: "./",
  // added to fix the build of https://github.com/NethServer/ns8-calnode/pull/114
  transpileDependencies: ["axios"],
  configureWebpack: {
    optimization: {
      splitChunks: {
        // Cap chunk size so the single vendor bundle is split into a few
        // cache-friendly chunks (not one ~2 MiB blob, not dozens of tiny files)
        maxSize: 500000,
      },
    },
  },
  chainWebpack: (config) => {
    config.module
      .rule("images")
      .use("url-loader")
      .loader("url-loader")
      .tap((options) => {
        // Do not base64 encode images URLs. Needed to always generate module logo image
        options.limit = -1;
        return options;
      });
  },
  css: {
    loaderOptions: {
      sass: {
        sassOptions: {
          silenceDeprecations: [
            "import",
            "global-builtin",
            "color-functions",
            "if-function",
            "legacy-js-api",
          ],
        },
      },
    },
  },
};
