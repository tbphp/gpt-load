import { createApp } from 'vue'

import App from './App.vue'
import { createAppRouter } from './app/router'
import './styles/tokens.css'
import './styles/base.css'

createApp(App).use(createAppRouter()).mount('#app')
