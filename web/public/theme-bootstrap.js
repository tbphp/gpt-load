;(function () {
  var theme

  try {
    theme = window.localStorage.getItem('gpt-load.theme')
  } catch {
    theme = undefined
  }

  if (theme === 'light' || theme === 'dark') {
    document.documentElement.dataset.theme = theme
    return
  }

  document.documentElement.removeAttribute('data-theme')
})()
