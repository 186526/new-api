/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useId } from 'react'
import { useController, type Control } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import { Badge } from '@/components/ui/badge'

import type { ProviderSummary } from '../lib/admin'
import {
  TOOL_ENDPOINTS,
  type GroupPolicyFormValues,
  type ToolContract,
} from '../lib/group-policies'
import { ProviderOrderEditor } from './ProviderOrderEditor'

type ToolEndpointRuleProps = {
  contract: ToolContract
  control: Control<GroupPolicyFormValues>
  providerHelpId: string
  directory?: ProviderSummary[]
  disabled: boolean
}

export function ToolEndpointRule(props: ToolEndpointRuleProps) {
  const { t } = useTranslation()
  const titleId = useId()
  const orderId = useId()
  const orderLabelId = useId()
  const errorId = useId()
  const modeHelpId = useId()
  const mode = useController({
    control: props.control,
    name: `rules.${props.contract}.mode`,
  })
  const order = useController({
    control: props.control,
    name: `rules.${props.contract}.order`,
  })
  const error = order.fieldState.error

  return (
    <div className='grid min-w-0 gap-3 py-3 @3xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] @3xl:items-start'>
      <h3
        className='flex flex-wrap items-center gap-2 text-sm font-medium @3xl:min-h-8'
      >
        <span>{props.contract.endsWith('/fetch') ? t('Fetch') : t('Search')}</span>
        <code id={titleId} dir='ltr' className='wrap-anywhere'>
          {TOOL_ENDPOINTS[props.contract]}
        </code>
        <Badge variant='outline'>
          {props.contract === 'alpha/search' && 'web_search_preview'}
          {props.contract !== 'alpha/search' &&
            (props.contract.endsWith('/fetch') ? 'tools-fetch' : 'tools-search')}
        </Badge>
      </h3>
      <FieldGroup className='min-w-0 gap-3'>
        <Field data-disabled={props.disabled}>
          <ToggleGroup
            aria-labelledby={titleId}
            disabled={props.disabled}
            value={[mode.field.value]}
            onValueChange={(value) => {
              if (value[0]) mode.field.onChange(value[0])
            }}
            variant='outline'
            className='max-w-full'
          >
            <ToggleGroupItem
              value='inherit'
              className='h-auto min-h-8 min-w-0 shrink whitespace-normal pointer-coarse:min-h-12'
            >
              {t('toolsApi.inherit')}
            </ToggleGroupItem>
            <ToggleGroupItem
              value='disabled'
              className='h-auto min-h-8 min-w-0 shrink whitespace-normal pointer-coarse:min-h-12'
            >
              {t('Disabled')}
            </ToggleGroupItem>
            <ToggleGroupItem
              value='prefer'
              className='h-auto min-h-8 min-w-0 shrink whitespace-normal pointer-coarse:min-h-12'
            >
              {t('toolsApi.prefer')}
            </ToggleGroupItem>
            <ToggleGroupItem
              value='only'
              className='h-auto min-h-8 min-w-0 shrink whitespace-normal pointer-coarse:min-h-12'
            >
              {t('Only listed providers')}
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
        {(mode.field.value === 'prefer' || mode.field.value === 'only') && (
          <Field data-invalid={!!error} data-disabled={props.disabled}>
            <FieldLabel id={orderLabelId} htmlFor={orderId}>
              {t('Provider order')}
            </FieldLabel>
            <ProviderOrderEditor
              id={orderId}
              ref={order.field.ref}
              aria-labelledby={`${orderLabelId} ${titleId}`}
              value={order.field.value}
              onChange={order.field.onChange}
              onBlur={order.field.onBlur}
              contract={props.contract}
              directory={props.directory}
              aria-invalid={!!error}
              aria-describedby={
                error
                  ? `${props.providerHelpId} ${modeHelpId} ${errorId}`
                  : `${props.providerHelpId} ${modeHelpId}`
              }
              disabled={props.disabled}
            />
            <p id={modeHelpId} className='text-muted-foreground text-sm'>
              {mode.field.value === 'only'
                ? t('Unlisted providers are never used.')
                : t('Other eligible providers can be used if the preferred providers are unavailable before sending.')}
            </p>
            {error && <FieldError id={errorId}>{error.message}</FieldError>}
          </Field>
        )}
      </FieldGroup>
    </div>
  )
}
