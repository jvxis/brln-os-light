# Rebalance: mínimo de exploração e descoberta por valor — 0.5.34

## Escopo

Dois mecanismos independentes, sem mudar AutoFee, Interlock, parâmetros
persistidos, autorizações de sources ou execução em produção:

1. A exploração do Sovereign com estoque pago ainda sem venda usa `Minimum`
   (`min_amount_sat`) como alvo, em vez de 10% do lote. A espera de observação,
   FIFO, ROI, lucro, orçamento e proteção contra reposição continuam valendo.
2. Uma resposta de rota inexistente para um valor não elimina imediatamente a
   possibilidade de uma rota menor na mesma source.

## Exploração por estoque

- Alvo: mínimo inicial do operador, elevado ao piso efetivo de execução se necessário.
- Nunca aumenta o lote original nem o déficit. O ajuste ao orçamento permanece.
- Sem split, `Minimum` também é o piso de execução. Com split, `Min execute` é o
  piso de execução e `Min probe` continua exclusivo das sondas de rota existentes.
- Exemplo: lote 300k, Minimum 50k, Min execute 10k → exploração-alvo 50k.
- Exemplo: lote 100k, Minimum/Min execute 1k → exploração-alvo 1k, não 10k.
- Não se altera automaticamente a configuração de nenhum node.
- Canais sem estoque em observação, jobs manuais e slots garantidos não recebem
  uma nova restrição de inventário.

## Busca menor na mesma source

Com `amount_probe_adaptive` ligado, os caminhos de consulta por source e
pré-passe MPP usam o mesmo fallback. Exemplo: 100k → 50k → 25k → 12,5k → 10k.
A busca para ao encontrar candidatos; a validação do canal de destino exato e
as verificações de rota/pagamento existentes continuam obrigatórias.

- No máximo cinco chamadas de descoberta, incluindo a original. Se a sequência
  for muito longa, a última consulta vai diretamente ao piso de execução.
- Compartilha o timeout já existente da tentativa/round/job; não renova prazos.
- Mantém source, destino, exclusões, Mission Control e limite de rota do caller.
- Recalcula o teto econômico para o valor menor e limita também pela proporção
  do teto absoluto original; não aumenta silenciosamente o ppm da fee ladder.
- Não repete falhas de transporte/autorização/cancelamento, políticas inválidas,
  destinos incorretos ou resultados ambíguos de pagamento.
- Só o resultado final da busca alimenta a falha de par/estrutural. Uma consulta
  de 100k sem rota não penaliza o par antes de tentar 50k.
- O delegated fast-path continua com seu fluxo atual; este fallback age na
  descoberta explícita por source depois dele e no pré-passe MPP.
- Sucessos menores seguem a contabilização de envio real, orçamento, aprendizado
  de par e continuação parcial existentes; não contam como o lote cheio.

Logs `no-route amount fallback` mostram source, target e valores intermediários.
No histórico de attempts aparece o valor final consultado/enviado. Não são
criadas invoices ou pagamentos para consultas malsucedidas de descoberta.

## Validação

Testes cobrem mínimos configurados, split ligado/desligado, déficit, orçamento,
ROI, espera de inventário, erro sem rota, sucesso no valor menor, teto de chamadas,
timeout compartilhado, cancelamento, erros não elegíveis, teto de fee inclusive
arredondamento/overflow, e preservação dos candidatos para validação exata do target.

Não se deduz que um valor menor terá sucesso; a mudança permite verificá-lo
dentro dos limites existentes.
