import { useRoute } from './lib/router.js'
import { Projects } from './views/Projects.jsx'
import { Project } from './views/Project.jsx'
import { RunView } from './views/RunView.jsx'
import { TraceView } from './views/TraceView.jsx'

// App is the router shell. Hash route → view.
export function App() {
  const route = useRoute()
  switch (route.name) {
    case 'trace':
      return <TraceView pid={route.pid} rid={route.rid} params={route.params} key={route.pid + '/' + route.rid + '/trace'} />
    case 'run':
      return <RunView pid={route.pid} rid={route.rid} key={route.pid + '/' + route.rid} />
    case 'project':
      return <Project pid={route.pid} key={route.pid} />
    default:
      return <Projects />
  }
}
