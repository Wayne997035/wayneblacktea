import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import zhTW from './zh-TW.json'
import en from './en.json'

const savedLang = localStorage.getItem('wbt-lang') ?? 'zh-TW'

void i18n
  .use(initReactI18next)
  .init({
    resources: {
      'zh-TW': { translation: zhTW },
      en: { translation: en },
    },
    lng: savedLang,
    fallbackLng: 'zh-TW',
    interpolation: {
      escapeValue: false,
    },
  })

export default i18n
