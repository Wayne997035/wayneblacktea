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

// jsdom 29 still ships without HTMLDialogElement.showModal/close — polyfill the
// minimum the modal tests need: open/close state and dispatching a `close` event.
//
// We deliberately do NOT auto-focus an [autofocus] element here (React's
// autoFocus prop already handles that during commit), and we do NOT restore
// focus to a captured trigger — each modal manages WCAG 2.4.3 focus return
// via its own ref so the behaviour works identically in jsdom and real browsers.
//
// This polyfill lives behind the standard prototype guard so it is a no-op on
// future jsdom versions that ship the native API.
const proto = HTMLDialogElement.prototype as unknown as {
  showModal?: (this: HTMLDialogElement) => void
  close?: (this: HTMLDialogElement, returnValue?: string) => void
}

if (typeof proto.showModal !== 'function') {
  proto.showModal = function showModal(this: HTMLDialogElement) {
    this.setAttribute('open', '')
  }
}

if (typeof proto.close !== 'function') {
  proto.close = function close(this: HTMLDialogElement) {
    if (!this.hasAttribute('open')) return
    this.removeAttribute('open')
    this.dispatchEvent(new Event('close'))
  }
}
