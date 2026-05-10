import '@testing-library/jest-dom'
import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from './i18n/en.json'
import zhTW from './i18n/zh-TW.json'

// Initialize a minimal i18next for tests (no localStorage / no DOM deps).
// English is the default test locale so existing assertions on rendered
// text remain stable; tests that need zh-TW can call i18next.changeLanguage.
if (!i18next.isInitialized) {
  void i18next.use(initReactI18next).init({
    resources: {
      en: { translation: en },
      'zh-TW': { translation: zhTW },
    },
    lng: 'en',
    fallbackLng: 'en',
    interpolation: { escapeValue: false },
  })
}
