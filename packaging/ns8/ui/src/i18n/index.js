export async function loadLanguage(lang) {
  try {
    const messages = await import(
      /* webpackChunkName: "lang-[request]" */ `../../public/i18n/${lang}/translation.json`
    );
    return messages;
  } catch (error) {
    console.warn(
      `Cannot import ${lang} language messages, falling back to English.`,
      error
    );

    // fallback to english
    if (lang !== "en") {
      return loadLanguage("en");
    }
  }
}
