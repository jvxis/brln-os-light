import { pathToFileURL } from 'node:url'
import { prepareDirectOrder } from './solana-orders.mjs'

export { directOrderRpc, prepareDirectOrder } from './solana-orders.mjs'

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    let input = ''
    process.stdin.setEncoding('utf8')
    for await (const chunk of process.stdin) {
      input += chunk
      if (input.length > 4096) throw new Error('Quote exceeds the maximum size')
    }
    const quote = JSON.parse(input)
    process.stdout.write(JSON.stringify(await prepareDirectOrder(quote)) + '\n')
  } catch (error) {
    process.stderr.write(`${error.message}\n`)
    process.exitCode = 1
  }
}
