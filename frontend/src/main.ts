import { createApp } from 'vue'
import { ElAlert, ElButton, ElCheckbox, ElDialog, ElEmpty, ElForm, ElFormItem, ElInput, ElInputNumber, ElOption, ElPagination, ElProgress, ElSelect, ElSkeleton, ElSwitch, ElTable, ElTableColumn, ElTag } from 'element-plus'
import 'element-plus/dist/index.css'
import App from './App.vue'
import './styles.css'
const app = createApp(App)
for (const [name, component] of Object.entries({ ElAlert, ElButton, ElCheckbox, ElDialog, ElEmpty, ElForm, ElFormItem, ElInput, ElInputNumber, ElOption, ElPagination, ElProgress, ElSelect, ElSkeleton, ElSwitch, ElTable, ElTableColumn, ElTag })) app.component(name, component)
app.mount('#app')
